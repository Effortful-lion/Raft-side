package labgob

//
// 尝试通过 RPC 发送非大写字段会导致一系列错误行为，
// 包括神秘的计算错误和直接崩溃。因此，这个围绕 Go 的 encoding/gob 的包装器
// 会警告非大写字段名。
//

import (
	"encoding/gob"
	"fmt"
	"io"
	"reflect"
	"sync"
	"unicode"
	"unicode/utf8"
)

var mu sync.Mutex                 // 保证原子性 - errorCount 和 checked
var errorCount int                // 小写字段的错误数量
var checked map[reflect.Type]bool // 检查过的类型

type LabEncoder struct {
	gob *gob.Encoder
}

func NewEncoder(w io.Writer) *LabEncoder {
	enc := &LabEncoder{}
	enc.gob = gob.NewEncoder(w)
	return enc
}

func (enc *LabEncoder) Encode(e interface{}) error {
	checkValue(e)
	return enc.gob.Encode(e)
}

func (enc *LabEncoder) EncodeValue(value reflect.Value) error {
	checkValue(value.Interface())
	return enc.gob.EncodeValue(value)
}

type LabDecoder struct {
	gob *gob.Decoder
}

func NewDecoder(r io.Reader) *LabDecoder {
	dec := &LabDecoder{}
	dec.gob = gob.NewDecoder(r)
	return dec
}

func (dec *LabDecoder) Decode(e interface{}) error {
	checkValue(e)
	checkDefault(e)
	return dec.gob.Decode(e)
}

// 注册自定义类型，比如： cmd 接口类型：Acmd Bcmd interface{}本身包含动态类型和值 gob无法识别它，所以先注册所有的动态类型
func Register(value interface{}) {
	checkValue(value)
	gob.Register(value)
}

func RegisterName(name string, value interface{}) {
	checkValue(value)
	gob.RegisterName(name, value)
}

// 便利接口，传入变量
func checkValue(value interface{}) {
	checkType(reflect.TypeOf(value))
}

// 实际的大小写检查 + 反射嵌套检查结构体字段
// 温习：
/*
1. reflect.Type - 具体的变量类型
2. reflect.Value - 具体的变量地址
3. Type.Kind - 变量类型的基础分类：struct、map、slice、int、 array、ptr等所有类型
4. Type.Kind() 和 Value.Kind() 完全相同 - 语义上/调用链路上、函数接收者不同
5. Elem：只适用于 容器/指针类型 生效，其他调用时panic「内部包裹的元素类型 Type.Elem ()」或「内部包裹的实际值 Value.Elem ()」
*/
func checkType(t reflect.Type) {
	// 获取变量的基础类型
	k := t.Kind()

	// 临界区
	mu.Lock()
	// only complain once, and avoid recursion.执行一次，避免递归
	if checked == nil {
		checked = map[reflect.Type]bool{}
	}
	// 检查过
	if checked[t] {
		mu.Unlock()
		return
	}
	// 未检查，标记检查
	checked[t] = true
	mu.Unlock()
	// 以上并发检查提高效率、锁保证并发安全

	// 根据不同的基础类型，
	switch k {
	case reflect.Struct:
		// 结构体类型：遍历每一个字段
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			// 拿出第一个字母
			rune, _ := utf8.DecodeRuneInString(f.Name)
			// 检查第一个字母为 全语言大写
			if unicode.IsUpper(rune) == false {
				// ta da
				fmt.Printf("labgob error: lower-case field %v of %v in RPC or persist/snapshot will break your Raft\n",
					f.Name, t.Name())
				mu.Lock()
				errorCount += 1
				mu.Unlock()
			}
			// 递归检查
			checkType(f.Type)
		}
		return
	case reflect.Slice, reflect.Array, reflect.Ptr:
		checkType(t.Elem())
		return
	case reflect.Map:
		checkType(t.Elem())
		checkType(t.Key())
		return
	default:
		return
	}
}

// 如果值包含非默认值，发出警告，
// 例如，如果发送了 RPC 但回复结构体已被修改。
// 如果 RPC 回复包含默认值，GOB 不会覆盖非默认值。
func checkDefault(value interface{}) {
	if value == nil {
		return
	}
	checkDefault1(reflect.ValueOf(value), 1, "")
}

// 检查"零值不覆盖"问题，如果有零值覆盖就警告
func checkDefault1(value reflect.Value, depth int, name string) {
	if depth > 3 {
		return
	}

	// 变量具体类型
	t := value.Type()
	// 该类型的基础类型
	k := t.Kind()

	switch k {
	case reflect.Struct:
		// 遍历所有字段值
		for i := 0; i < t.NumField(); i++ {
			// 具体字段值
			vv := value.Field(i)
			// 结构体字段的层级名
			name1 := t.Field(i).Name
			if name != "" {
				name1 = name + "." + name1
			}
			// 值嵌套检查、递归深度+1、检查层级
			checkDefault1(vv, depth+1, name1)
		}
		return
	case reflect.Ptr:
		if value.IsNil() {
			return
		}
		checkDefault1(value.Elem(), depth+1, name)
		return
		// 最终的检查点：Go 原生基础类型 默认值检查
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr, reflect.Float32, reflect.Float64,
		reflect.String:
		// 当前值类型的零值 == 当前值 比较
		if reflect.DeepEqual(reflect.Zero(t).Interface(), value.Interface()) == false {
			// 非默认值
			mu.Lock()
			if errorCount < 1 {
				what := name
				if what == "" {
					what = t.Name()
				}
				// 此警告通常在以下情况下出现：代码重复使用相同的 RPC 回复
				// 变量进行多次 RPC 调用，或者代码将持久化状态恢复到
				// 已经具有非默认值的变量中。
				fmt.Printf("labgob warning: Decoding into a non-default variable/field %v may not work\n",
					what)
			}
			errorCount += 1
			mu.Unlock()
		}
		return
	}
}
