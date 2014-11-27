## Hamsdr

Software-defined radio scanner. 

_**NOTE:** work in progress._

### Building

Rtlsdr C library is required. Most Linux distros include `rtl-sdr` and `rtl-sdr-devel` packages.

Go is required. Most popular Linux distro ship a `golang` package. Once Go is installed: 

- run `go get github.com/porjo/hamsdr` 
- cd $GOPATH/src/github.com/porjo/hamsdr
- run `go build`

This will produce a `hamsdr` binary which can be [used like](http://kmkeen.com/rtl-demod-guide/) `rtl_fm`

### Credits

- Code based on `rtl_fm` from [rtl-sdr](https://github.com/keenerd/rtl-sdr) by Kyle Keen (@keenerd).
