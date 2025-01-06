package main

import (
	"encoding/json"
	"go/ast"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var decoratorBinaryPath = os.Getenv("GOPATH") + "/bin/decorator"

type _packageInfo struct {
	Dir, // 包所在的目录路径
	ImportPath, // 包的导入路径
	Name, // 包的名称
	Target,
	Root, // Go 项目的根目录
	StaleReason string // 包过时的原因
	Stale  bool // 包是否是过时的
	Module struct {
		Main  bool // 否是主模块
		Path, // 模块路径
		Dir, // 模块目录
		GoMod, // Mod 文件路径
		GoVersion string // Go 版本
	}
	Match,
	GoFiles, // Go 源文件列表
	Imports, // TODO remove -find
	Deps []string // TODO remove -find
}

// 获取当前项目或指定路径下的包信息
//
// 通过执行 go list -json -find 命令来获取包信息，并解析 JSON 输出。
// 具体步骤如下：
//   - 根据传入的 pkgPath 参数构造命令行。如果 pkgPath 不为空且不等于 "main"，则将其作为包路径传递给 go list 命令。
//   - 使用 exec.Command 执行该命令并获取输出。
//   - 将输出的 JSON 数据解析为 _packageInfo 结构体实例并返回。
func getPackageInfo(pkgPath string) (*_packageInfo, error) {
	command := []string{"go", "list", "-json", "-find"}
	if pkgPath != "" && pkgPath != "main" {
		command = append(command, pkgPath)
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = projectDir
	cmd.Env = os.Environ()
	bf, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	p := &_packageInfo{}
	err = json.Unmarshal(bf, p)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// importer 结构体用于存储 Go 文件中的导入信息，具体包括：
//   - nameMap：导入名称（如别名）到包路径的映射。
//   - pathMap：从包路径到导入名称（如别名）的映射。
//   - pathObjMap：从包路径到 ast.ImportSpec 对象的映射，用于进一步处理 AST（抽象语法树）。
type importer struct {
	nameMap    map[string]string
	pathMap    map[string]string
	pathObjMap map[string]*ast.ImportSpec
}

// 示例 1：无别名导入
//
//	import "github.com/user/project"
//
//	pkg = "github.com/user/project"
//	extName = "project"
//	name = extName 因为 ip.Name 为 nil
//
//	更新映射：
//	nameMap["project"] = "github.com/user/project"
//	pathMap["github.com/user/project"] = "project"
//	pathObjMap["github.com/user/project"] = ip
//
// 示例 2：带别名导入
//
//	import alias "github.com/user/project"
//
//	pkg = "github.com/user/project"
//	extName = "project"
//	name = "alias" 因为 ip.Name.Name = "alias"
//
// 更新映射：
//
//	nameMap["alias"] = "github.com/user/project"
//	pathMap["github.com/user/project"] = "alias"
//	pathObjMap["github.com/user/project"] = ip
//
// 示例 3：忽略导入
//
//	import _ "github.com/user/project"
//
//	pkg = "github.com/user/project"
//	extName = "project"
//	name = extName 因为 ip.Name.Name = "_"
//
//	更新映射：
//	nameMap["project"] = "github.com/user/project"
//	pathMap["github.com/user/project"] = "_"
//	pathObjMap["github.com/user/project"] = ip
//
// 示例 4：点导入
//
//	import . "github.com/user/project"
//
//	pkg = "github.com/user/project"
//	extName = "project"
//	name = extName 因为 ip.Name.Name = "."
//
// 更新映射：
//
//	nameMap["project"] = "github.com/user/project"
//	pathMap["github.com/user/project"] = "."
//	pathObjMap["github.com/user/project"] = ip
//
// 示例 5：版本号大于 1
//
//	import "github.com/user/project/v2"
//
//	pkg = "github.com/user/project/v2"
//	extName = "v2"
//
//	因为 extName 以 v 开头且版本号大于 1，所以：
//	arr = ["github.com", "user", "project", "v2"]
//	extName = arr[len(arr)-2] = "project"
//	name = extName = "project"
//
//	更新映射：
//	nameMap["project"] = "github.com/user/project/v2"
//	pathMap["github.com/user/project/v2"] = "project"
//	pathObjMap["github.com/user/project/v2"] = ip
//
// 示例 6：版本号等于 1
//
//	import "github.com/user/project/v1"
//
//	pkg = "github.com/user/project/v1"
//	extName = "v1"
//	因为版本号等于 1，不会改变 extName
//	name = extName = "v1"
//
//	更新映射：
//	nameMap["v1"] = "github.com/user/project/v1"
//	pathMap["github.com/user/project/v1"] = "v1"
//	pathObjMap["github.com/user/project/v1"] = ip
//
// 示例 7：无版本号
//
//	import "github.com/user/project"
//
//	pkg = "github.com/user/project"
//	extName = "project"
//	name = extName = "project"
//
//	更新映射：
//	nameMap["project"] = "github.com/user/project"
//	pathMap["github.com/user/project"] = "project"
//	pathObjMap["github.com/user/project"] = ip
func newImporter(f *ast.File) *importer {
	nameMap := map[string]string{}             // 存储导入别名到包路径的映射
	pathMap := map[string]string{}             // 存储包路径到导入名称（别名）的映射
	pathObjMap := map[string]*ast.ImportSpec{} // 存储包路径到 AST 中导入项的映射

	// 检查文件中的导入声明是否存在
	if f.Imports != nil && len(f.Imports) > 0 {
		// 遍历每个导入项
		for _, ip := range f.Imports {
			// 跳过空的导入声明
			if ip == nil {
				continue
			}

			// 我们从路径中提取出包名，忽略版本号或文件扩展名。
			//  github.com/user/project/v2 => v2 ，在后面确定包名时会忽略 v2 而重置为 project
			//	github.com/user/project/file.go => file
			// 	github.com/user/project => project
			//
			// 假设 ip.Path.Value 是以下字符串：
			//
			//	"github.com/user/project/v2"
			//	- strconv.Unquote 去掉引号，得到：github.com/user/project/v2
			//	- filepath.Base(pkg) 提取最后一部分路径：v2
			//	- filepath.Ext(pkg) 提取扩展名：""（因为没有扩展名）
			//	- strings.TrimRight 去掉扩展名，结果：v2
			//
			//	"github.com/user/project/file.go"
			//	- strconv.Unquote 去掉引号，得到：github.com/user/project/file.go
			//	- filepath.Base(pkg) 提取最后一部分路径：file.go
			//	- filepath.Ext(pkg) 提取扩展名：.go
			//	- strings.TrimRight 去掉扩展名，结果：file
			//
			//	"github.com/user/project"
			//	- strconv.Unquote 去掉引号，得到：github.com/user/project
			//	- filepath.Base(pkg) 提取最后一部分路径：project
			//	- filepath.Ext(pkg) 提取扩展名：""（因为没有扩展名）
			//	- strings.TrimRight 去掉扩展名，结果：project
			pkg, _ := strconv.Unquote(ip.Path.Value)
			extName := strings.TrimRight(filepath.Base(pkg), filepath.Ext(pkg))

			// 如果包路径中包含版本号并且版本号大于 1（如 v2、v3 等），则将包路径中的版本号去掉，只保留版本号之前的部分作为包的基本名称；
			// 例如：pkg/v2 → pkg 。如果版本号是 v1 或者没有版本号，则不会进行任何更改，包的基本名称保持不变。
			//
			// case1: 版本号 v2
			// 	pkg := "github.com/example/pkg/v2"
			//	extName := "v2"
			//
			// 	strings.HasPrefix("v2", "v")  // 返回 true
			// 	strings.TrimLeft("v2", "v")   // 返回 "2"
			// 	strconv.Atoi("2")             // 返回 2, err == nil, 2 > 1, 返回 true
			//
			//  arr := strings.Split(pkg, "/")  // ["github.com", "example", "pkg", "v2"]
			//	extName = arr[len(arr)-2]       // "pkg"
			//
			// case2: 版本号 v1
			// 	pkg := "github.com/example/pkg/v1"
			//	extName := "v1"
			//
			//	strings.HasPrefix("v1", "v")  // 返回 true
			//	strings.TrimLeft("v1", "v")    // 返回 "1"
			//	strconv.Atoi("1")             // 返回 1, err == nil, 1 > 1, 返回 false
			//
			//	匿名函数返回 false，不进入 `if` 语句块，extName 不做改变，保持为 v1
			//
			// case3: 没有版本号
			//	pkg := "github.com/example/pkg"
			//	extName := "pkg"
			//
			//	strings.HasPrefix("pkg", "v")  // 返回 false
			if strings.HasPrefix(extName, "v") && func() bool {
				v, err := strconv.Atoi(strings.TrimLeft(extName, "v"))
				return err == nil && v > 1
			}() {
				arr := strings.Split(pkg, "/")
				if len(arr) > 1 {
					extName = arr[len(arr)-2]
				}
			}

			// 根据导入语句的不同形式决定 name 的值。这里是对常见 import 语句形式的处理：
			//	- 无别名：直接使用包路径的最后一部分作为包名。
			//	- 别名为空字符串：也是默认使用包路径的最后一部分作为包名。
			//	- _ 导入：包名仍然是路径的最后一部分，但包不会被直接引用。
			//	- . 导入：包名仍然是路径的最后一部分，包中的符号直接暴露给当前作用域。
			//	- 指定别名：使用指定的别名。
			//
			// Case1
			//	import "github.com/user/project"
			//	ip.Name 为 nil
			//	extName 为 project
			//
			// 表示导入语句没有指定别名，在这种情况下，name 被设置为 extName，即 project
			//
			// Case2
			//	import _ "github.com/user/project"
			//	ip.Name.Name 为 "_"
			//	extName 为 project
			//
			// 表示包被导入但未直接使用，常用于执行包中的初始化函数。包名本身不会被引用。
			// 在这种情况下，name 仍然被设置为 extName，但这个包只是为了执行初始化函数，而不会被其他地方直接引用。
			//
			// Case3
			//	import . "github.com/user/project"
			//	ip.Name.Name 为 "."
			//	extName 为 project
			//
			// 包被导入并且使用该包中的符号直接访问。这样，你可以直接使用包中的函数或变量名，而不需要加上包名前缀。
			// 在这种情况下，name 仍然被赋值为 extName，但这个包中的符号将直接暴露给当前的作用域。
			//
			// Case4
			//	import alias "github.com/user/project"
			//	ip.Name.Name 为 "alias"
			//	name = ip.Name.Name，结果为 alias
			var name string
			if ip.Name == nil {
				// import path/name // name form pkg
				name = extName
			} else {
				switch ip.Name.Name {
				case "":
					// import path/name // name form pkg
					name = extName
				case "_":
					// import _ path/name // name pkg, about to be replaced
					name = extName
				case ".":
					// import . path/name // ""
					name = extName
				default:
					// import yname path/name // yname from alias
					name = ip.Name.Name
				}
			}

			nameMap[name] = pkg
			pathObjMap[pkg] = ip
			pathMap[pkg] = func() string {
				if ip.Name != nil {
					return ip.Name.Name
				}
				return name
			}()
		}
	}
	return &importer{
		nameMap:    nameMap,
		pathMap:    pathMap,
		pathObjMap: pathObjMap,
	}
}

// 根据导入名称查询包路径
func (i *importer) importedName(name string) (pat string, ok bool) {
	pat, ok = i.nameMap[name]
	return
}

// 根据包路径查询导入名称
func (i *importer) importedPath(pkg string) (name string, ok bool) {
	name, ok = i.pathMap[pkg]
	return
}
