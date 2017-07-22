## Hamsdr

Software-defined radio scanner.

### Requirements

* rtl-sdr library

### Running

Grab one of the [pre-compiled binaries](https://github.com/porjo/hamsdr/releases) for Linux (amd64 or Arm), or compile from source as described below.

The `hamsdr` binary is used in the same way as [rtl_fm](http://kmkeen.com/rtl-demod-guide/) e.g.

```
hamsdr -M wbfm -f 89.1M | play -r 32k -t raw -e s -b 16 -c 1 -V1 -
```

### Building

Rtl-sdr C library is required. Most Linux distros include `rtl-sdr` and `rtl-sdr-devel` packages, unfortunately they are quite out of date which causes the build to fail - you will need to grab the latest source.

#### Building rtl-sdr

My prefered way of building is as follows:

- download latest RTL SDR source: `git clone https://github.com/librtlsdr/librtlsdr`
- build and install RTL SDR to /usr/local
```
$ cd librtlsdr
$ mkdir build
$ cd build
$ cmake -DCMAKE_INSTALL_PREFIX:PATH=/usr/local/rtl-sdr ../
$ make
$ sudo make install
```

#### Building Hamsdr

- `go get github.com/porjo/hamsdr`
- ignore compile error: `fatal error: rtl-sdr.h: No such file or directory`
- cd `$GOPATH/src/github.com/porjo/hamsdr`
- build Hamsdr against RTL SDR:
```
$ CGO_LDFLAGS="-lrtlsdr -L/usr/local/rtl-sdr/lib" \
  CGO_CPPFLAGS="-I/usr/local/rtl-sdr/include"  \
  go build
```
- Find `hamsdr` binary in current directory


### Credits

- Code based on `rtl_fm` from [rtl-sdr](https://github.com/keenerd/rtl-sdr) by Kyle Keen (@keenerd).
