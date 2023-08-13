package main

import (
	"github.com/dengsgo/go-decorator/cmd/logs"
	"github.com/dengsgo/go-decorator/decor"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const (
	decoratorScanFlag    = "//go:decor "
	decoratorPackagePath = "github.com/dengsgo/go-decorator/decor"
)

var (
	tempDir          = path.Join(os.TempDir(), "gobuild_decorator_works")
	tempGenDir       = tempDir
	projectDir, _    = os.Getwd()
	workspaceCleaner = func() {}
)

func init() {
	initUseFlag()
	if err := os.MkdirAll(tempDir, 0666); err != nil {
		logs.Error("Init() fail, os.MkdirAll tempDir", err)
	}
}

func main() {
	if len(os.Args) < 3 {
		logs.Error("os.Args < 3")
	}
	//logs.Log.Level = logs.LevelInfo
	logs.Debug("os.Args", os.Args)
	chainName := os.Args[1]
	chainArgs := os.Args[2:]
	toolName := filepath.Base(chainName)

	var err error
	switch strings.TrimSuffix(toolName, ".exe") {
	case "compile":
		err = compile(chainArgs)
	case "link":
		link(chainArgs)
		defer func() {
			logs.Debug("workspaceCleaner() begin")
			workspaceCleaner()
			logs.Debug("workspaceCleaner() end")
		}()
	}

	if err != nil {
		logs.Error(err)
	}
	// build
	cmd := exec.Command(chainName, chainArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if cmd.Run() != nil {
		logs.Error("run toolchain err", chainName, err)
	}
}

//go:decor logging
func test(v ...string) string {
	return ""
}

func logging(ctx *decor.Context) {
	ctx.TargetDo()
}

// ###############################

//func myFuncDecor(a int, b string) (_decorGenOut1 int, _decorGenOut2 int) {
//	decor := &DecorContext{
//		WarpFuncIn:  []any{a, b},
//		WarpFuncOut: []any{_decorGenOut1, _decorGenOut2},
//	}
//	decor.Func = func() {
//		decor.WarpFuncOut[0], decor.WarpFuncOut[1] = func(a int, b string) (int, int) {
//			log.Println("Func: myFunc", b)
//			return a, a + 1
//		}(decor.WarpFuncIn[0].(int), decor.WarpFuncIn[1].(string))
//	}
//	logging(decor)
//	return decor.WarpFuncOut[0].(int), decor.WarpFuncOut[1].(int)
//}
