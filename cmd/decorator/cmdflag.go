package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/dengsgo/go-decorator/cmd/logs"
)

const version = `v0.22.0 beta`
const opensourceUrl = `https://github.com/dengsgo/go-decorator`

// CmdFlag 存储命令行参数，包括日志级别、临时目录、是否清理工作目录、程序版本号等。
type CmdFlag struct {
	Level     string // -d.log          // 指定日志级别
	TempDir   string // -d.tempDir		// 指定工作目录
	ClearWork bool   // -d.clearWork	// 完成编译后是否清理工作目录
	Version   string // -version		// 程序版本号

	// go build args
	toolPath  string   // 存储当前执行的工具路径，即运行此程序的命令。
	chainName string   // 保存工具链的名称（从命令行参数中解析得出）
	chainArgs []string // 存储工具链后续的命令行参数（chainArgs 是工具链的参数列表）。
}

func initUseFlag() {
	// 将命令行参数 -d.log 映射到 cmdFlag.Level 字段，并且给它一个默认值 warn 。
	flag.StringVar(&cmdFlag.Level,
		"d.log",
		"warn",
		"output log level. all/debug/info/warn/error/close")
	// 将命令行参数 -d.tempDir 映射到 cmdFlag.TempDir，它定义了工作目录的路径。如果没有提供该参数，默认使用空字符串。
	flag.StringVar(&cmdFlag.TempDir,
		"d.tempDir",
		"",
		"tool workspace dir. default same as go build workspace")
	// 将命令行参数 -d.clearWork 映射到 cmdFlag.ClearWork，决定是否在编译完成后清理工作空间。
	flag.BoolVar(&cmdFlag.ClearWork,
		"d.clearWork",
		true,
		"empty workspace when compilation is complete")
	// 如果命令行输入 -h 或 --help，会输出这段自定义的帮助信息。
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s:\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "decorator [-d.log] [-d.tempDir] chainToolPath chainArgs\n")
		flag.PrintDefaults()
	}
	// 解析命令行参数
	flag.Parse()

	// 设置日志级别
	switch cmdFlag.Level {
	case "all":
		logs.Log.Level = logs.LevelAll
	case "debug":
		logs.Log.Level = logs.LevelDebug
	case "info":
		logs.Log.Level = logs.LevelInfo
	case "warn":
		logs.Log.Level = logs.LevelWarn
	case "error", "":
		logs.Log.Level = logs.LevelError
	case "close":
		logs.Log.Level = logs.LevelClose
	}
	log.SetPrefix("decorator: ") // 设置日志前缀为 "decorator: "
	if logs.Log.Level < logs.LevelDebug {
		log.SetFlags(0)
	}

	// 设置临时目录
	if cmdFlag.TempDir != "" {
		tempDir = cmdFlag.TempDir // TODO check
	}

	// 获取工具链路径和参数
	cmdFlag.toolPath = os.Args[0]       // 获取当前程序的执行路径。
	goToolDir := os.Getenv("GOTOOLDIR") // 获取环境变量 GOTOOLDIR 的值。
	if goToolDir == "" {
		logs.Info("env key `GOTOOLDIR` not found")
	}
	if len(os.Args) < 2 {
		fmt.Fprintf(flag.CommandLine.Output(), "decorator %s , %s\n", version, opensourceUrl)
		os.Exit(0)
	}

	// 遍历命令行参数，检查是否存在以 goToolDir 为前缀的路径。
	// 如果找到了这个路径，则认为它是工具链的路径，并将其赋值给 cmdFlag.chainName 。
	// 剩余的参数（如果有的话）会被赋值到 cmdFlag.chainArgs 中。
	for i, arg := range os.Args[1:] {
		if goToolDir != "" && strings.HasPrefix(arg, goToolDir) {
			cmdFlag.chainName = arg
			if len(os.Args[1:]) > i+1 {
				cmdFlag.chainArgs = os.Args[i+2:]
			}
			break
		}
	}
}

var cmdFlag = &CmdFlag{}
