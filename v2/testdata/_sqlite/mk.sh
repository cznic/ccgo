set -e
rm -f log-ccgo
make distclean || true
make clean || true
./configure CC=ccgo \
	CFLAGS='--ccgo-full-paths --ccgo-struct-checks --ccgo-go -I../tcl8.6.8/generic -Doff64_t=off_t -Dpread64=pread --ccgo-use-import os.DevNull,exec.Command --ccgo-import os,os/exec' \
	LDFLAGS='--warn-unresolved-libs --warn-go-build'
make tcltest
