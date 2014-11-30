## Hamsdr

Software-defined radio scanner. 

_**NOTE:** work in progress._

### Requirements

* rtl-sdr library

### Running

Grab one of the [pre-compiled binaries](https://github.com/porjo/hamsdr/releases) for Linux (amd64 or Arm), or compile from source as described below.

The `hamsdr` binary is used in the same was as [rtl_fm](http://kmkeen.com/rtl-demod-guide/) e.g.

```
hamsdr -M wbfm -f 89.1M | play -r 32k -t raw -e s -b 16 -c 1 -V1 -
```

### Building

Rtl-sdr C library is required. Most Linux distros include `rtl-sdr` and `rtl-sdr-devel` packages.

Go is required. Most popular Linux distro ship a `golang` package. Once Go is installed: 

- `go get github.com/porjo/hamsdr` 
- cd $GOPATH/src/github.com/porjo/hamsdr
- `go build`


### Credits

- Code based on `rtl_fm` from [rtl-sdr](https://github.com/keenerd/rtl-sdr) by Kyle Keen (@keenerd).
