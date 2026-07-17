package testdata

import "embed"

//go:embed workloads/*
var Content embed.FS
