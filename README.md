## Hamsdr

Software-defined radio scanner. Code based on `rtl_fm.c` from the [rtlsdr project](http://sdr.osmocom.org/trac/wiki/rtl-sdr).

Requires rtlsdr library

**NOTE:** work in progress. Currently outputs static audio only.

### Building

Go is required. Most popular Linux distro ship a golang package.

Once Go is installed: 

- run `go get github.com/porjo/hamsdr` 
- cd $GOPATH/src/github.com/porjo/hamsdr
- run `go build`

This will produce a `hamsdr` binary which can be used like `rtl_fm`
