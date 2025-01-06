package decor

// This file defines the context required for the decorator.
//
// If the function defined is of type func (* decor. Context), it is a decorator function,
// Can be used to decorate any top-level function.
//
// On top-level functions, use decorator functions through go single line annotations.
// For example:
//
// ```go
//   //go:decor decorHandlerFunc
//   func myFunc1() {
//	   log.Println("call myFunc1")
//   }
// ```
//
// The function myFunc1 declares the use of the decorator decorHandlerFunc The `go-decorator` tool
// will rewrite the target to inject decorHandlerFunc code during compilation.
// All of this is automatically completed at compile time!

// TKind is target types above and below the decorator
type TKind uint8

const (
	KFunc   TKind = iota // top-level function   // 顶级函数
	KMethod              // method				// 成员方法
)

// Context The context of the decorator.
//
// The input and output parameters of the target function and the execution of
// the target method can be obtained through this context.
//
// Use TargetDo() to call the target function.
// If TargetDo() is not called in the decorator function, it means that the target
// function will not be called, even if you call the decorated target function in your code!
// At this point, the objective function returns zero values.
//
// Before TargetDo(), you can modify TargetIn to change the input parameter values.
// After TargetDo(), you can modify TargetOut to change the return value.
//
// You can only change the value of the input and output parameters. Don't try to change
// their type and quantity, as this will trigger runtime panic!!!
//
// Context 提供了装饰器所需的所有信息，包括输入参数、输出结果、目标函数名称等。
type Context struct {
	// Target types above and below the decorator
	// 目标类型: 函数 or 方法
	Kind TKind

	// The input parameters of the decorated function
	// 入参列表，它是一个 []any 类型，表示可以接受任意类型的输入参数。
	TargetIn,

	// TargetOut : The result parameters of the decorated function
	// 输出结果，它是一个 []any 类型，表示可以接受任意类型的返回值。
	TargetOut []any

	// The function or method name of the target
	// 目标名称
	TargetName string

	// If Kind is 'KMethod', it is the Receiver of the target
	// 如果目标是一个方法，这里保存该方法的接收者（即方法所属的对象）。如果目标是函数，则该字段为 nil。
	Receiver any

	// The Non-parameter Packaging of the Objective Function // inner
	Func func()

	// The number of times the objective function was called
	// 记录目标函数被调用的次数。
	doRef int64
}

// TargetDo : Call the target function.
//
// Calling this method once will automatically increment doRef by 1.
//
// Any problem can trigger panic, and a good habit is to capture it
// in the decorator function.
func (d *Context) TargetDo() {
	d.doRef++
	d.Func()
}

// DoRef gets the number of times an anonymous wrapper class has been executed.
// Usually, it shows the number of times TargetDo() was called in the decorator function.
func (d *Context) DoRef() int64 {
	return d.doRef
}
