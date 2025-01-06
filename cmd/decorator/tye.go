package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"
)

// 四种比较运算符：gt（大于），gte（大于等于），lt（小于），lte（小于等于）。
type lintComparableKey = string

const (
	lintCpGt  lintComparableKey = "gt"
	lintCpGte lintComparableKey = "gte"
	lintCpLt  lintComparableKey = "lt"
	lintCpLte lintComparableKey = "lte"
)

var (
	// 将字符串映射到 types.BasicInfo ，用于确定参数的基本类型（如整数、浮点数、字符串等）。
	decorOptionParamTypeMap = map[string]types.BasicInfo{
		"bool": types.IsBoolean,

		"int":    types.IsInteger,
		"int8":   types.IsInteger,
		"in16":   types.IsInteger,
		"int32":  types.IsInteger,
		"int64":  types.IsInteger,
		"unit":   types.IsInteger,
		"unit8":  types.IsInteger,
		"unit16": types.IsInteger,
		"unit32": types.IsInteger,
		"unit64": types.IsInteger,

		"float32": types.IsFloat,
		"float64": types.IsFloat,

		"string": types.IsString,
	}

	// 标记哪些比较操作符（如 gt、gte 等）是允许的，用于后续的比较验证。
	lintRequiredRangeAllowKeyMap = map[lintComparableKey]bool{
		lintCpGt:  true,
		lintCpGte: true,
		lintCpLt:  true,
		lintCpLte: true,
	}
)

type pkgSet struct {
	fset *token.FileSet
	pkgs map[string]*ast.Package
}

// 对标准库 map 做了封装
type mapV[K comparable, V any] struct {
	items map[K]V
}

func newMapV[K comparable, V any]() *mapV[K, V] {
	return &mapV[K, V]{
		items: make(map[K]V),
	}
}

// 如果已经存在返回 false ，否则返回 true
func (m *mapV[K, V]) put(key K, value V) bool {
	if _, ok := m.items[key]; ok {
		return false
	}
	m.items[key] = value
	return true
}

// 不存在返回默认值
func (m *mapV[K, V]) get(key K) (v V) {
	if v, ok := m.items[key]; ok {
		return v
	}
	return
}

// 装饰器的注解：
//   - doc：装饰器的文档注释。
//   - name：装饰器的名称。
//   - parameters：装饰器参数。
type decorAnnotation struct {
	doc        *ast.Comment      // ast node for doc
	name       string            // decorator name
	parameters map[string]string // options parameters
}

func newDecorAnnotation(doc *ast.Comment, name string, parameters map[string]string) *decorAnnotation {
	return &decorAnnotation{
		doc:        doc,
		name:       name,
		parameters: parameters,
	}
}

func (d *decorAnnotation) splitName() []string {
	// 将装饰器名称按 . 分割成一个字符串数组，方便解析装饰器的层级结构。
	return strings.Split(d.name, ".")
}

// 装饰器的参数：
//   - index: 参数的位置索引。
//   - name: 参数的名称。
//   - typ: 参数的类型，参考 decorOptionParamTypeMap 的 keys 。
//   - required: 一个指向 requiredLinter 的指针，用于验证该参数是否符合必需的规则。
//   - nonzero: 是否需要该参数为非零值。
type decorArg struct {
	index int
	name,
	typ string
	// decor lint rule
	required *requiredLinter
	nonzero  bool
}

// 根据参数的类型返回对应的 types.BasicInfo。
func (d *decorArg) typeKind() types.BasicInfo {
	if t, ok := decorOptionParamTypeMap[d.typ]; ok {
		return t
	}
	return types.IsUntyped
}

// 根据装饰器参数的 required 规则验证参数值是否符合取值范围或合法枚举值。
func (d *decorArg) passRequiredLint(value string) error {
	// 如果没有设置 `required` 规则，直接返回 nil，不做任何验证。
	if d.required == nil {
		return nil
	}
	// 1. 检查传入的值是否在允许的枚举值中，如果不在枚举值中，返回错误信息。
	if !d.required.inEnum(value) {
		return errors.New(fmt.Sprintf("lint: key '%s' value '%s' can't pass lint enum", d.name, value))
	}
	// 2. 如果没有设置比较规则，则不需要进行进一步的验证，直接返回 nil。
	if d.required.compare == nil {
		return nil
	}

	// 3. 将传入的 `value` 转换为浮点类型（`float64`），用于后续的比较。
	val := 0.0
	if d.typeKind() == types.IsString {
		// 如果参数类型是 `string`，则将字符串的长度(去掉字符串的引号)作为比较值。
		val = float64(len(value) - 2)
	} else {
		// 如果参数类型是数值类型（`int`、`float` 等），则直接转换为浮点类型。
		val, _ = strconv.ParseFloat(value, 64)
	}

	// 4. 定义一个比较函数，根据给定的比较操作符（`gt`, `gte`, `lt`, `lte`）进行比较。
	compare := func(c lintComparableKey, v float64) bool {
		switch c {
		case lintCpGt:
			return val > v
		case lintCpGte:
			return val >= v
		case lintCpLt:
			return val < v
		case lintCpLte:
			return val <= v
		}
		return true
	}
	// 5. 遍历所有的比较规则，检查 `value` 是否满足每个规则。
	for c, v := range d.required.compare {
		if !compare(c, v) {
			return errors.New(fmt.Sprintf("lint: key '%s' value '%s' can't pass lint %s:%v", d.name, value, c, v))
		}
	}
	// 6. 如果全部验证通过，返回 `nil`。
	return nil
}

// 检查参数值是否为零，如果 nonzero 为 true，则要求参数值不能为零。
func (d *decorArg) passNonzeroLint(value string) error {
	isZero := func() bool {
		switch d.typeKind() {
		case types.IsInteger, types.IsFloat:
			value, _ := strconv.ParseFloat(value, 64)
			return value == 0
		case types.IsString:
			return value == `""`
		case types.IsBoolean:
			return value == "false"
		}
		return false
	}
	if d.nonzero && isZero() {
		return errors.New(fmt.Sprintf("lint: key '%s' value '%s' can't pass nonzero lint", d.name, value))
	}
	return nil
}

// 装饰器参数的名称与 decorArg 结构体的映射。
type decorArgsMap map[string]*decorArg

// 定义参数的验证规则，包括：
//   - compare: 一个映射，表示允许的比较操作符和相应的数值。
//   - enum: 一个字符串切片，表示允许的枚举值。
type requiredLinter struct {
	//	gt,
	//	gte,
	//	le,
	//	lte
	compare map[lintComparableKey]float64
	enum    []string
}

func (r *requiredLinter) initCompare() {
	if r.compare == nil {
		r.compare = map[lintComparableKey]float64{}
	}
}

func (r *requiredLinter) inEnum(value string) bool {
	if r.enum == nil {
		return true
	}
	for _, v := range r.enum {
		if v == value {
			return true
		}
	}
	return false
}
