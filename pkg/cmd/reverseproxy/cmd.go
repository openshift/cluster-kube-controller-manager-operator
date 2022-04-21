package reverseproxy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/crypto"
)

// reverseProxyOpts holds values to run the proxy.
type reverseProxyOpts struct {
	bindAddress string
	certFile    string
	keyFile     string

	kcmPort         string
	kcmCABundleFile string

	cpcPort         string
	cpcCABundleFile string
	cpcPathPrefix   string
}

const kcmPathPrefix = "/"

// NewReverseProxyCommand creates a render command.
func NewReverseProxyCommand() *cobra.Command {
	proxyOpts := &reverseProxyOpts{}
	cmd := &cobra.Command{
		Use:   "reverse-proxy",
		Short: "Start Reverse Proxy for Kube Controller Manager and Cluster Policy Controller to be able to serve metrics on the same port\"",
		Run: func(cmd *cobra.Command, args []string) {
			if err := proxyOpts.Validate(); err != nil {
				klog.Fatal(err)
			}
			if err := proxyOpts.Run(); err != nil {
				klog.Fatal(err)
			}
		},
	}

	proxyOpts.AddFlags(cmd.Flags())

	return cmd
}

func (r *reverseProxyOpts) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&r.bindAddress, "listen", r.bindAddress, "The ip:port to serve on.")
	fs.StringVar(&r.certFile, "tls-cert-file", r.certFile, "serving certificate file (defaults to a generated cert)")
	fs.StringVar(&r.keyFile, "tls-private-key-file", r.keyFile, "serving certificate key file (defaults to a generated cert)")

	fs.StringVar(&r.kcmPort, "kcm-port", r.kcmPort, "Kube Controller Manager port")
	fs.StringVar(&r.kcmCABundleFile, "kcm-cabundle-file", r.kcmCABundleFile, "Kube Controller Manager CA bundle certificates")

	fs.StringVar(&r.cpcPort, "cpc-port", r.cpcPort, "Cluster Policy Controller port")
	fs.StringVar(&r.cpcCABundleFile, "cpc-cabundle-file", r.cpcCABundleFile, "Cluster Policy Controller CA bundle certificates")
	fs.StringVar(&r.cpcPathPrefix, "cpc-url-path-prefix", r.cpcPathPrefix, "URL path prefix for Cluster Policy Controller")
}

// Validate verifies the inputs.
func (r *reverseProxyOpts) Validate() error {
	if len(r.bindAddress) == 0 {
		return errors.New("missing required flag: --listen")
	}

	hasCertFile, hasKeyFile := len(r.certFile) == 0, len(r.keyFile) == 0
	if hasCertFile != hasKeyFile {
		return errors.New("incorrect flags: eiter both --tls-cert-file and --tls-private-key-file are required for setting serving cert, or none to auto-generate the serving cert")
	}

	if len(r.kcmPort) == 0 {
		return errors.New("missing required flag: --kcm-port")
	}
	if len(r.kcmCABundleFile) == 0 {
		return errors.New("missing required flag: --kcm-cabundle-file")
	}

	if len(r.cpcPort) == 0 {
		return errors.New("missing required flag: --cpc-port")
	}
	if len(r.cpcCABundleFile) == 0 {
		return errors.New("missing required flag: --cpc-cabundle-file")
	}
	if len(r.cpcPathPrefix) == 0 {
		return errors.New("missing required flag: --cpc-url-path-prefix")
	}
	return nil
}

// Run contains the logic of the proxy command.
func (r *reverseProxyOpts) Run() error {
	if len(r.certFile) == 0 || len(r.keyFile) == 0 {
		err := r.generateSelfSignedCert()
		if err != nil {
			return err
		}
	}

	kcmURL, err := url.Parse("https://" + net.JoinHostPort("127.0.0.1", r.kcmPort))
	if err != nil {
		return err
	}
	cpcURL, err := url.Parse("https://" + net.JoinHostPort("127.0.0.1", r.cpcPort))
	if err != nil {
		return err
	}

	kcmProxy, err := newReverseProxy(kcmURL, r.kcmCABundleFile, kcmPathPrefix)
	if err != nil {
		return err
	}
	kcmHandler := reverseProxyHandler(kcmProxy)

	cpcProxy, err := newReverseProxy(cpcURL, r.cpcCABundleFile, r.cpcPathPrefix)
	if err != nil {
		return err
	}
	cpcHandler := reverseProxyHandler(cpcProxy)

	mux := http.NewServeMux()

	mux.HandleFunc(kcmPathPrefix, kcmHandler)
	mux.HandleFunc(r.cpcPathPrefix, cpcHandler)
	mux.HandleFunc(r.cpcPathPrefix+"/", cpcHandler)

	srv := &http.Server{Addr: r.bindAddress, Handler: mux}
	defer srv.Close()

	klog.Infof("Listening on %v", r.bindAddress)
	return srv.ListenAndServeTLS(r.certFile, r.keyFile)
}

func (r *reverseProxyOpts) generateSelfSignedCert() error {
	klog.Warningf("Using insecure, self-signed certificates")

	temporaryCertDir, err := ioutil.TempDir("", "reverse-proxy-serving-cert-")
	if err != nil {
		return err
	}
	signerName := fmt.Sprintf("reverse-proxy-signer@%d", time.Now().Unix())
	ca, err := crypto.MakeSelfSignedCA(
		filepath.Join(temporaryCertDir, "serving-signer.crt"),
		filepath.Join(temporaryCertDir, "serving-signer.key"),
		filepath.Join(temporaryCertDir, "serving-signer.serial"),
		signerName,
		0,
	)
	if err != nil {
		return err
	}

	r.certFile = filepath.Join(temporaryCertDir, "tls.crt")
	r.keyFile = filepath.Join(temporaryCertDir, "tls.key")
	// nothing can trust this, so we don't really care about hostnames
	servingCert, err := ca.MakeServerCert(sets.NewString("localhost", "127.0.0.1"), 30)
	if err != nil {
		return err
	}
	if err := servingCert.WriteCertConfigFile(r.certFile, r.keyFile); err != nil {
		return err
	}

	return nil
}

func newReverseProxy(target *url.URL, caBundleFile string, pathPrefix string) (*httputil.ReverseProxy, error) {
	if target.Path != "" || target.RawPath != "" || target.RawQuery != "" {
		return nil, errors.New("ReverseProxy Director only supports URL without a path")
	}
	proxy := &httputil.ReverseProxy{}

	rootCAs := x509.NewCertPool()

	caCert, err := ioutil.ReadFile(caBundleFile)
	if err != nil {
		return nil, err
	}

	if !rootCAs.AppendCertsFromPEM(caCert) {
		return nil, errors.New("could not append caCert to root CAs")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		RootCAs: rootCAs,
	}
	proxy.Transport = transport

	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		if len(pathPrefix) != 0 && pathPrefix != "/" {
			req.URL.Path = strings.TrimPrefix(req.URL.Path, pathPrefix)
			if len(req.URL.RawPath) != 0 {
				req.URL.RawPath = strings.TrimPrefix(req.URL.EscapedPath(), pathPrefix)
			}
		}
		if _, ok := req.Header["User-Agent"]; !ok {
			// explicitly disable User-Agent so it's not set to default value
			req.Header.Set("User-Agent", "")
		}
	}

	return proxy, nil
}

func reverseProxyHandler(proxy *httputil.ReverseProxy) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	}
}
