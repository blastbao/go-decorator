package main

import (
	"github.com/dengsgo/go-decorator/cmd/logs"
	"os"
	"path/filepath"
	"strings"
)

func link(args []string) {
	var cfg string
	buildmode := false

	// 遍历 args 中的每个参数 arg
	for _, arg := range args {
		// 将 buildmode 设置为 true，指定构建模式为可执行文件或位置无关可执行文件。
		if arg == "-buildmode=exe" || arg == "-buildmode=pie" /* windows*/ {
			buildmode = true
		}
		// 如果参数以 - 开头，继续下一个参数（跳过选项参数）
		if strings.HasPrefix(arg, "-") {
			continue
		}
		// 检查参数是否包含路径 b001/importcfg.link ，如果包含，认为这是配置文件路径，将其赋值给 cfg 。
		if strings.Contains(arg, filepath.Join("b001", "importcfg.link")) {
			cfg = arg
		}
	}

	// 日志打印
	logs.Debug("cfg", cfg)

	// 如果 buildmode 为 false 或 cfg 为空，则直接返回，不进行后续操作。
	if !buildmode || cfg == "" {
		return
	}

	// 如果 cmdFlag.ClearWork 为 true，定义 exitDo 函数用于清理临时目录 tempDir。
	if cmdFlag.ClearWork {
		exitDo = func() {
			_ = os.RemoveAll(tempDir)
		}
	}
}
