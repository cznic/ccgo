// Copyright 2017 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccgo

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"go/scanner"
	"go/token"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/cznic/cc"
	"github.com/cznic/ccir"
	"github.com/cznic/internal/buffer"
	"github.com/cznic/ir"
	"github.com/cznic/strutil"
	"github.com/cznic/xc"
)

func caller(s string, va ...interface{}) {
	if s == "" {
		s = strings.Repeat("%v ", len(va))
	}
	_, fn, fl, _ := runtime.Caller(2)
	fmt.Fprintf(os.Stderr, "# caller: %s:%d: ", path.Base(fn), fl)
	fmt.Fprintf(os.Stderr, s, va...)
	fmt.Fprintln(os.Stderr)
	_, fn, fl, _ = runtime.Caller(1)
	fmt.Fprintf(os.Stderr, "# \tcallee: %s:%d: ", path.Base(fn), fl)
	fmt.Fprintln(os.Stderr)
	os.Stderr.Sync()
}

func dbg(s string, va ...interface{}) {
	if s == "" {
		s = strings.Repeat("%v ", len(va))
	}
	_, fn, fl, _ := runtime.Caller(1)
	fmt.Fprintf(os.Stderr, "# dbg %s:%d: ", path.Base(fn), fl)
	fmt.Fprintf(os.Stderr, s, va...)
	fmt.Fprintln(os.Stderr)
	os.Stderr.Sync()
}

func use(...interface{}) {}

func init() {
	use(caller, dbg, TODO) //TODOOK
	flag.BoolVar(&Testing, "testing", false, "")
}

// ============================================================================

var (
	ccTestdata string

	cpp     = flag.Bool("cpp", false, "")
	filter  = flag.String("re", "", "")
	ndebug  = flag.Bool("ndebug", false, "")
	noexec  = flag.Bool("noexec", false, "")
	oLog    = flag.Bool("log", false, "")
	trace   = flag.Bool("trc", false, "")
	yydebug = flag.Int("yydebug", 0, "")
)

func init() {
	ip, err := cc.ImportPath()
	if err != nil {
		panic(err)
	}

	for _, v := range filepath.SplitList(strutil.Gopath()) {
		p := filepath.Join(v, "src", ip, "testdata")
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}

		if fi.IsDir() {
			ccTestdata = p
			break
		}
	}
	if ccTestdata == "" {
		panic("cannot find cc/testdata/")
	}
}

func errStr(err error) string {
	switch x := err.(type) {
	case scanner.ErrorList:
		if len(x) != 1 {
			x.RemoveMultiples()
		}
		var b bytes.Buffer
		for i, v := range x {
			if i != 0 {
				b.WriteByte('\n')
			}
			b.WriteString(v.Error())
			if i == 9 {
				fmt.Fprintf(&b, "\n\t... and %v more errors", len(x)-10)
				break
			}
		}
		return b.String()
	default:
		return err.Error()
	}
}

func parse(src []string, opts ...cc.Opt) (_ *cc.TranslationUnit, err error) {
	defer func() {
		if e := recover(); e != nil && err == nil {
			err = fmt.Errorf("cc.Parse: PANIC: %v\n%s", e, debug.Stack())
		}
	}()

	model, err := ccir.NewModel()
	if err != nil {
		return nil, err
	}

	var ndbg string
	if *ndebug {
		ndbg = "#define NDEBUG 1"
	}
	ast, err := cc.Parse(fmt.Sprintf(`
%s
#define __arch__ %s
#define __os__ %s
#include <builtin.h>

#define NO_TRAMPOLINES 1
`, ndbg, runtime.GOARCH, runtime.GOOS),
		src,
		model,
		opts...,
	)
	if err != nil {
		return nil, fmt.Errorf("cc.Parse: %v", errStr(err))
	}

	return ast, nil
}

func expect1(wd, match string, hook func(string, string) []string, opts ...cc.Opt) (log buffer.Bytes, exitStatus int, err error) {
	var lpos token.Position
	if *cpp {
		opts = append(opts, cc.Cpp(func(toks []xc.Token) {
			if len(toks) != 0 {
				p := toks[0].Position()
				if p.Filename != lpos.Filename {
					fmt.Fprintf(&log, "# %d %q\n", p.Line, p.Filename)
				}
				lpos = p
			}
			for _, v := range toks {
				log.WriteString(cc.TokSrc(v))
			}
			log.WriteByte('\n')
		}))
	}
	if n := *yydebug; n != -1 {
		opts = append(opts, cc.YyDebug(n))
	}
	ast, err := parse([]string{ccir.CRT0Path, match}, opts...)
	if err != nil {
		return log, -1, err
	}

	var out, src buffer.Bytes
	if err := New(ast, &out, func(f *ir.FunctionDefinition) string {
		return "" //TODO
	}); err != nil {
		return log, -1, fmt.Errorf("New: %v", err)
	}

	fmt.Fprintf(&src, `package main

func main() {
	X_start(0, 0) //TODO188
}

%s`, out.Bytes())
	b, err := format.Source(src.Bytes())
	if err != nil {
		b = src.Bytes()
	}
	fmt.Fprintf(&log, "# ccgo.New\n%s", b)
	if err != nil {
		return log, exitStatus, err
	}

	if *noexec {
		return log, 0, nil
	}

	var stdout, stderr buffer.Bytes

	defer func() {
		stdout.Close()
		stderr.Close()
	}()

	if err := func() (err error) {
		defer func() {
			if e := recover(); e != nil && err == nil {
				err = fmt.Errorf("exec: PANIC: %v", e)
			}
		}()

		vwd, err := ioutil.TempDir("", "ccgo-test-")
		if err != nil {
			return err
		}

		if err := os.Chdir(vwd); err != nil {
			return err
		}

		defer func() {
			os.Chdir(wd)
			os.RemoveAll(vwd)
		}()

		if err := ioutil.WriteFile("main.go", b, 0664); err != nil {
			return err
		}

		args := hook(vwd, match)
		cmd := exec.Command("go", append([]string{"run", "main.go"}, args[1:]...)...)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if b := stdout.Bytes(); b != nil {
				fmt.Fprintf(&log, "stdout:\n%s\n", b)
			}
			if b := stderr.Bytes(); b != nil {
				fmt.Fprintf(&log, "stderr:\n%s\n", b)
			}
			return fmt.Errorf("go run: exit status %v, err %v", exitStatus, err)
		}

		return nil
	}(); err != nil {
		return log, 1, err
	}

	if b := stdout.Bytes(); b != nil {
		fmt.Fprintf(&log, "stdout:\n%s\n", b)
	}
	if b := stderr.Bytes(); b != nil {
		fmt.Fprintf(&log, "stderr:\n%s\n", b)
	}

	//TODO expect := match[:len(match)-len(filepath.Ext(match))] + ".expect"
	//TODO if _, err := os.Stat(expect); err != nil {
	//TODO 	if !os.IsNotExist(err) {
	//TODO 		return log, 0, err
	//TODO 	}

	//TODO 	return log, 0, nil
	//TODO }

	//TODO buf, err := ioutil.ReadFile(expect)
	//TODO if err != nil {
	//TODO 	return log, 0, err
	//TODO }

	//TODO if g, e := stdout.Bytes(), buf; !bytes.Equal(g, e) {
	//TODO 	return log, 0, fmt.Errorf("==== %v\n==== got\n%s==== exp\n%s", match, g, e)
	//TODO }
	//TODO return log, 0, nil
	return log, 0, nil
}

func expect(t *testing.T, dir string, skip func(string) bool, hook func(string, string) []string, opts ...cc.Opt) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.c"))
	if err != nil {
		t.Fatal(err)
	}

	seq := 0
	okSeq := 0
	for _, match := range matches {
		if skip(match) {
			continue
		}

		if *trace {
			fmt.Println(match)
		}
		seq++
		doLog := *oLog
		log, exitStatus, err := expect1(wd, match, hook, opts...)
		switch {
		case exitStatus <= 0 && err == nil:
			okSeq++
		default:
			if seq-okSeq == 1 {
				t.Logf("%s: FAIL\n%s\n%s", match, errStr(err), log.Bytes())
				doLog = false
			}
		}
		if doLog {
			t.Logf("%s:\n%s", match, log.Bytes())
		}
		log.Close()
	}
	t.Logf("%v/%v ok", okSeq, seq)
	if okSeq != seq {
		t.Errorf("failures: %v", seq-okSeq)
	}
}

func TestTCC(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	testdata, err := filepath.Rel(wd, ccTestdata)
	if err != nil {
		t.Fatal(err)
	}

	var re *regexp.Regexp
	if s := *filter; s != "" {
		re = regexp.MustCompile(s)
	}

	dir := filepath.Join(testdata, filepath.FromSlash("tcc-0.9.26/tests/tests2/"))
	expect(
		t,
		dir,
		func(match string) bool {
			if re != nil && !re.MatchString(filepath.Base(match)) {
				return true
			}

			return false
		},
		func(wd, match string) []string {
			switch filepath.Base(match) {
			case "31_args.c":
				return []string{"./test", "-", "arg1", "arg2", "arg3", "arg4"}
			case "46_grep.c":
				ioutil.WriteFile(filepath.Join(wd, "test"), []byte("abc\ndef\nghi\n"), 0600)
				return []string{"./grep", "[ea]", "test"}
			default:
				return []string{match}
			}
		},
		//TODO- cc.EnableAnonymousStructFields(),
		//TODO- cc.EnableDefineOmitCommaBeforeDDD(),
		//TODO- cc.ErrLimit(-1),
		cc.AllowCompatibleTypedefRedefinitions(),
		cc.EnableImplicitFuncDef(),
		cc.KeepComments(),
		cc.SysIncludePaths([]string{ccir.LibcIncludePath}),
	)
}
