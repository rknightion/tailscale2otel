package main

import "golang.org/x/text/unicode/norm"

var _ = norm.NFC.Bytes([]byte("ac3"))
