package main

import (
	"github.com/dengsgo/go-decorator/example/usages/g"
	"log"
	"time"
)

func main() {
	// 这是一个使用包内装饰器的函数
	useScopeInnerDecor("hello, world", 100)

	// 这是一个使用其他包装饰器的函数
	useExternalaDecor()
	g.PrintfLn("plus(2, 3) = %+v", plus(2, 3))

	// 文档 Guide.md 中演示使用装饰器的代码
	{
		t := 1692450000
		s := datetime(t)
		g.Printf("datetime(%d)=%s\n", t, s)
	}

	// 泛型函数使用装饰器
	g.PrintfLn("Sum(1, 2, 3, 4, 5, 6, 7, 8, 9) = %+v", Sum(1, 2, 3, 4, 5, 6, 7, 8, 9))

	// 使用带有参数的装饰器，如何传值
	g.PrintfLn("useArgsDecor() = %+v", useArgsDecor())
	// 装饰器如何使用 Lint 在编译时约束验证目标函数的参数
	g.Printf("useHitUseRequiredLint() = %+v", useHitUseRequiredLint())
	g.Printf("useHitUseNonzeroLint() = %+v", useHitUseNonzeroLint())
	g.Printf("useHitBothUseLint() = %+v", useHitBothUseLint())
	g.Printf("useHitUseMultilineLintDecor() = %+v", useHitUseMultilineLintDecor())
}

func init() {
	log.SetFlags(0)
	time.Local = time.FixedZone("CST", 8*3600)
}
