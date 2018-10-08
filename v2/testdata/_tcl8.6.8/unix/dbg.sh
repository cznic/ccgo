set -e
rm -f ./tcltest
make tcltest
echo -n > log-ccgo
LD_LIBRARY_PATH=`pwd`: TCLLIBPATH="/home/jnml/tmp/tcl8.6.8/unix/pkgs" TCL_LIBRARY="/home/jnml/tmp/tcl8.6.8/library" ./tcltest /home/jnml/tmp/tcl8.6.8/tests/all.tcl -file append.test -match append-1.1
