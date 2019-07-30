package targetconfigcontroller

import (
	"strings"
	"testing"
)

func TestIsRequiredConfigPresent(t *testing.T) {
	tests := []struct {
		name          string
		config        string
		expectedError string
	}{
		{
			name: "unparseable",
			config: `{
		 "servingInfo": {
		}
		`,
			expectedError: "error parsing config",
		},
		{
			name:          "empty",
			config:        ``,
			expectedError: "no observedConfig",
		},
		{
			name: "null-cluster-name",
			config: `{
			 "extendedArguments": {
			   "cluster-name": null
			 }
		 }
		`,
			expectedError: "extendedArguments.cluster-name null in config",
		},
		{
			name: "missing-cluster-name",
			config: `{
			 "extendedArguments": {
			   "cluster-name": []
			 }
		 }
		`,
			expectedError: "extendedArguments.cluster-name empty in config",
		},
		{
			name: "empty-string-cluster-name",
			config: `{
			 "extendedArguments": {
			   "cluster-name": ""
			 }
		 }
        `,
			expectedError: "extendedArguments.cluster-name empty in config",
		},
		{
			name: "good",
			config: `{
			 "extendedArguments": {
			   "cluster-name": ["some-name"]
			 }
		 }
		`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := isRequiredConfigPresent([]byte(test.config))
			switch {
			case actual == nil && len(test.expectedError) == 0:
			case actual == nil && len(test.expectedError) != 0:
				t.Fatal(actual)
			case actual != nil && len(test.expectedError) == 0:
				t.Fatal(actual)
			case actual != nil && len(test.expectedError) != 0 && !strings.Contains(actual.Error(), test.expectedError):
				t.Fatal(actual)
			}
		})
	}
}
