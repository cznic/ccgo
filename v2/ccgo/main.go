// Copyright 2018 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command ccgo is a C compiler targeting Go.
package main


/*

jnml@r550:~/src/github.com/ossrs/librtmp> export GOARCH=amd64 ; make clean && make CC=ccgo
rm -f *.o rtmpdump rtmpgw rtmpsrv rtmpsuck
/home/jnml/src/github.com/ossrs/librtmp/librtmp
make[1]: Entering directory '/home/jnml/src/github.com/ossrs/librtmp/librtmp'
rm -f *.o *.a *.so *.so.1 librtmp.pc
make[1]: Leaving directory '/home/jnml/src/github.com/ossrs/librtmp/librtmp'
/home/jnml/src/github.com/ossrs/librtmp/librtmp
make[1]: Entering directory '/home/jnml/src/github.com/ossrs/librtmp/librtmp'
ccgo -Wall   -DRTMPDUMP_VERSION=\"v2.4\" -DUSE_OPENSSL  -O2 -fPIC   -c -o rtmp.o rtmp.c
ccgo -Wall   -DRTMPDUMP_VERSION=\"v2.4\" -DUSE_OPENSSL  -O2 -fPIC   -c -o log.o log.c
ccgo -Wall   -DRTMPDUMP_VERSION=\"v2.4\" -DUSE_OPENSSL  -O2 -fPIC   -c -o amf.o amf.c
ccgo -Wall   -DRTMPDUMP_VERSION=\"v2.4\" -DUSE_OPENSSL  -O2 -fPIC   -c -o hashswf.o hashswf.c
ccgo -Wall   -DRTMPDUMP_VERSION=\"v2.4\" -DUSE_OPENSSL  -O2 -fPIC   -c -o parseurl.o parseurl.c
ar rs librtmp.a rtmp.o log.o amf.o hashswf.o parseurl.o
ar: creating librtmp.a
ccgo -shared -Wl,-soname,librtmp.so.1  -o librtmp.so.1 rtmp.o log.o amf.o hashswf.o parseurl.o  -lssl -lcrypto -lz 
ccgo: error: unrecognized command line option "-shared"
Makefile:92: recipe for target 'librtmp.so.1' failed
make[1]: *** [librtmp.so.1] Error 2
make[1]: Leaving directory '/home/jnml/src/github.com/ossrs/librtmp/librtmp'
Makefile:76: recipe for target 'librtmp/librtmp.a' failed
make: *** [librtmp/librtmp.a] Error 2
jnml@r550:~/src/github.com/ossrs/librtmp> export GOARCH=arm ; make clean && make CC=ccgo
rm -f *.o rtmpdump rtmpgw rtmpsrv rtmpsuck
/home/jnml/src/github.com/ossrs/librtmp/librtmp
make[1]: Entering directory '/home/jnml/src/github.com/ossrs/librtmp/librtmp'
rm -f *.o *.a *.so *.so.1 librtmp.pc
make[1]: Leaving directory '/home/jnml/src/github.com/ossrs/librtmp/librtmp'
/home/jnml/src/github.com/ossrs/librtmp/librtmp
make[1]: Entering directory '/home/jnml/src/github.com/ossrs/librtmp/librtmp'
ccgo -Wall   -DRTMPDUMP_VERSION=\"v2.4\" -DUSE_OPENSSL  -O2 -fPIC   -c -o rtmp.o rtmp.c
ccgo -Wall   -DRTMPDUMP_VERSION=\"v2.4\" -DUSE_OPENSSL  -O2 -fPIC   -c -o log.o log.c
ccgo -Wall   -DRTMPDUMP_VERSION=\"v2.4\" -DUSE_OPENSSL  -O2 -fPIC   -c -o amf.o amf.c
ccgo -Wall   -DRTMPDUMP_VERSION=\"v2.4\" -DUSE_OPENSSL  -O2 -fPIC   -c -o hashswf.o hashswf.c
ccgo -Wall   -DRTMPDUMP_VERSION=\"v2.4\" -DUSE_OPENSSL  -O2 -fPIC   -c -o parseurl.o parseurl.c
ar rs librtmp.a rtmp.o log.o amf.o hashswf.o parseurl.o
ar: creating librtmp.a
ccgo -shared -Wl,-soname,librtmp.so.1  -o librtmp.so.1 rtmp.o log.o amf.o hashswf.o parseurl.o  -lssl -lcrypto -lz 
ccgo: error: unrecognized command line option "-shared"
Makefile:92: recipe for target 'librtmp.so.1' failed
make[1]: *** [librtmp.so.1] Error 2
make[1]: Leaving directory '/home/jnml/src/github.com/ossrs/librtmp/librtmp'
Makefile:76: recipe for target 'librtmp/librtmp.a' failed
make: *** [librtmp/librtmp.a] Error 2
jnml@r550:~/src/github.com/ossrs/librtmp> 

*/

//TODO libtool (ccgo: error: unrecognized command line option "-shared")
//TODO must be able to handle libssl, libcrypto and libz (zlib?)

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/cznic/cc/v2"
	"github.com/cznic/ccgo/v2"
)

func main() {
	r, err := main1(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, strings.TrimSpace(err.Error()))
	}
	os.Exit(r)
}

type config struct {
	D        []string
	arg0     string
	args     []string
	c        bool
	goarch   string
	goos     string
	incPaths []string
	o        string
	sysPaths []string
}

func newConfig(args []string) (*config, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no arguments to parse")
	}

	r := &config{
		arg0:     args[0],
		goarch:   env("GOARCH", runtime.GOARCH),
		goos:     env("GOOS", runtime.GOOS),
		incPaths: []string{"@"},
	}
	args = args[1:]
	for len(args) != 0 {
		switch arg := args[0]; {
		case strings.HasPrefix(arg, "-D"):
			a := strings.SplitN(arg, "=", 2)
			if len(a) == 1 {
				a = append(a, "1")
			}
			r.D = append(r.D, fmt.Sprintf("%s %s", a[0][2:], a[1]))
		case strings.HasPrefix(arg, "-c"): // Compile and assemble, but do not link
			r.c = true
		case strings.HasPrefix(arg, "-o"): // Place the output into <file>
			if len(args) < 2 {
				return nil, fmt.Errorf("-o option requires an argument")
			}

			r.o = args[1]
			args = args[1:]
		case !strings.HasPrefix(arg, "-"):
			r.args = append(r.args, arg)
		case
			strings.HasPrefix(arg, "-O"),
			strings.HasPrefix(arg, "-W"),
			strings.HasPrefix(arg, "-f"):

			// ignored
		default:
			return nil, fmt.Errorf("%s: error: unrecognized command line option %q", r.arg0, arg)
		}
		args = args[1:]
	}
	return r, nil
}

func main1(args []string) (r int, err error) {
	returned := false

	defer func() {
		e := recover()
		if !returned && r == 0 {
			err = fmt.Errorf("PANIC: %v\n%s", e, debug.Stack())
		}
	}()

	c, err := newConfig(args)
	if err != nil {
		return 2, err
	}

	if len(c.args) == 0 {
		return 2, fmt.Errorf(`
%s: fatal error: no input files
compilation terminated`, c.arg0)
	}

	if c.sysPaths, err = cc.Paths(true); err != nil {
		return 1, err
	}

	sysPaths, err := cc.Paths(false)
	if err != nil {
		return 1, err
	}

	c.sysPaths = append(c.sysPaths, sysPaths...)
	for _, arg := range c.args {
		switch ext := filepath.Ext(arg); ext {
		case ".c":
			src, err := cc.NewFileSource2(arg, true)
			if err != nil {
				return 1, err
			}

			defs := []string{`
#define _DEFAULT_SOURCE 1
#define _POSIX_C_SOURCE 200809
#define _POSIX_SOURCE 1
#define __FUNCTION__ __func__ // gcc compatibility
#define __ccgo__ 1
`}
			for _, v := range c.D {
				defs = append(defs, fmt.Sprintf("#define %s", v))
			}

			builtin, err := cc.Builtin()
			if err != nil {
				return 1, err
			}
			tweaks := &cc.Tweaks{
				// TrackExpand:   func(s string) { fmt.Print(s) },
				// TrackIncludes: func(s string) { fmt.Printf("[#include %s]\n", s) },
				EnableAnonymousStructFields: true,
				InjectFinalNL:               true,
			}
			tu, err := cc.Translate(tweaks, c.incPaths, c.sysPaths, []cc.Source{
				cc.NewStringSource("<defines>", strings.Join(defs, "\n")),
				builtin,
				src,
			}...)
			if err != nil {
				return 1, expandError(err)
			}

			switch {
			case c.c: // Compile and assemble, but do not link
				fn := toExt(arg, ".o")
				if c.o != "" {
					if len(c.args) > 1 {
						return 1, fmt.Errorf("-o cannot be used with multiple input files")
					}

					fn = c.o
				}

				func() {
					f, err := os.Create(fn)
					if err != nil {
						return
					}

					defer func() {
						if e := f.Close(); e != nil && err == nil {
							err = e
						}
					}()

					b := bufio.NewWriter(f)

					defer func() {
						if e := b.Flush(); e != nil && err == nil {
							err = e
						}
					}()

					err = ccgo.Object(b, c.goos, c.goarch, tu)
				}()

				if err != nil {
					return 1, err
				}
			default:
				panic("TODO")
			}
		default:
			return 1, fmt.Errorf("%s: file not recognized", arg)
		}
	}

	returned = true
	return 0, nil
}
