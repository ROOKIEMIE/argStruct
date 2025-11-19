# argStruct

## 描述

这是一个使用结构体驱动的、通过成员tag便可以实现简易配置的、基于Go语言的、Cli命令解析库

## 参数配置

该库使用Go结构体以及结构体成员Tag进行参数配置，内部将根据Tag配置将Source中的值反射至对应的结构体成员中。

### 参数定义

> 参数结构体示例

```go
type Root struct {
    Proxy string `alias:"p, proxy" desc:"Proxy url or host"`
}

type Sub1 struct {
    Username string `alias:"u,username" desc:"user name input" required:"" default:"admin"`
    Count int `alias:"c, count" desc:"count of something"`
}

type Sub2 struct {
    Password string `alias:"pwd,password" desc:"this is pwd desc" required:"" default:"admin"`
    Interval float64 `alias:"inter" desc:"this is interval desc"`
}

type Sub3 struct {
    DeviceId string `alias:"did, device-id" desc:"this is device id desc"`
}
```

### 参数校验

库在扫描参数结构体配置时，会检测该参数结构体是否实现了一个名为**Validate**的方法，若实现了，将认为该方法为该参数结构体的校验方法，在最终GetOption时会调用其进行**返回前**的参数填充校验。

该方法在库中的定义如下所示：

```go
type validateProvider interface {
	Validate(ctx *ValidationContext) error
}
```

该方法的入参**ValidationContext**类型的指针将由库自动填充，该参数的主要作用是帮助使用者在编写参数结构体的参数校验时可以获取到该参数结构体的父结构体或任意祖结构体，以便于进行复杂的参数校验。该参数对外提供了以下方法：

```go
// Self 当前这一层（通常是 *YourStruct）
func (ctx *ValidationContext) Self() any {
	if ctx == nil {
		return nil
	}
	return ctx.self
}

// Root 整个 CLI 的 root（通常是 *Root）
func (ctx *ValidationContext) Root() any {
	if ctx == nil {
		return nil
	}
	return ctx.root
}

// Parent 直接父节点（父 struct 所在的 context），最顶层 root 的 Parent() 为 nil
func (ctx *ValidationContext) Parent() *ValidationContext {
	if ctx == nil {
		return nil
	}
	return ctx.parent
}

// Path 当前节点的逻辑路径，比如 "Root.Sub1.Sub2"
func (ctx *ValidationContext) Path() string {
	if ctx == nil {
		return ""
	}
	return ctx.path
}

// WalkUp 链式向上遍历，从 Self 开始一路走到 Root。
// fn 返回 true 表示“停止遍历”。
func (ctx *ValidationContext) WalkUp(fn func(c *ValidationContext) bool) {
	for cur := ctx; cur != nil; cur = cur.parent {
		if fn(cur) {
			return
		}
	}
}

// FindAncestor 查找链上的第一个满足类型 T 的节点
func FindAncestor[T any](ctx *ValidationContext) *T {
	var target *T
	ctx.WalkUp(func(c *ValidationContext) bool {
		if v, ok := c.Self().(*T); ok {
			target = v
			return true
		}
		return false
	})
	return target
}
```

> 使用示例

现假设：

```txt
Root(Proxy) <==> Sub1(Username){} <==> Sub2(Password){} <==> Sub3{}
```

上述假设的意思是，Sub3配置挂载在Sub2的Password成员下，Sub2配置挂载在Sub1的Username成员下，Sub1配置挂载在Root的Proxy成员下。结构如下所示：

```bash
# 简化图
Root:
	- Proxy <==> Sub1:
					 - Username <==> Sub2:
					 					 - Password <==> Sub3:
					 					 					 - DeviceId
					 					 - Internal
					 - Count
```

现在Sub3中配置校验方法，如下所示：

```go
func (sub3 *Sub3) Validate(ctx *ValidationContext) error {
    Sub1 := FindAncestor[Sub1](ctx)
    if Sub1.Count > 1 && Sub1.Count < 10 {
        if sub3.DeviceId == "" {
            return fmt.Errorf("Sub1.Count isn't valid, valid range is (1, 10), Sub3.DeviceId is empty config")
        }
    } else {
        return fmt.Errorf("Sub1.Count isn't valid, valid range is (1, 10)")
    }
    return nil
}
```

当**ValidateTOnly**为**true**时，仅会进行GetOption所传入的T自身的校验；当**ValidateTOnly**为**false**时，将会从T的root参数结构体开始进行校验。无论进行哪种校验路径，都将先进行required tag校验，再进行Validate方法的自定义校验。

以下给出一个简图以便于你理解该过程：

```bash
# ValidateTOnly = false
GetOption[Sub2](nil)
# Validator
Root <== Check Requred Config
Root.Validate(ctx)
	Sub1 <== Check Requred Config
	Sub1.Validate(ctx)
		Sub2 <== Check Requred Config
		Sub2.Validate(ctx)
		# If not error
		return *Sub2{}

# ValidateTOnly = true
GetOption[Sub2](&argStruct.ParserConfig{ValidateTOnly: true})
# Validator
Sub2 <== Check Requred Config
Sub2.Validate(ctx)
# If not error
return *Sub2{}
```

### 帮助信息

库在扫描参数结构体配置时，会检测该参数结构体是否实现了一个名为**Usage**的方法，若实现了，将认为该方法为该参数结构体的帮助方法。

该方法在库中的定义如下所示：

```go
type usageProvider interface {
	Usage() string
}
```

与参数校验不同，在大部分时候都需要打印完整的帮助信息，因此库提供了一个对外函数以方便获取完整的帮助信息字符串：

```go
// Usage 对外使用的帮助函数
func Usage(config *ParserConfig) string {
	style := StyleShortDash
	if config != nil && config.ArgsStyle != 0 {
		style = config.ArgsStyle
	}
	return globalSchema.usage(style)
}
```

对于配置于参数结构体的**Usage**方法所返回的字符串目前支持在其中插入模板，目前已有的模板参数如下所示：

```txt
${Field} -> 显示该字段的“主 alias”或完整 flag 形式
$[Field] -> 显示该字段的描述（desc）
$<Field> -> 显示 REQUIRED/optional 标记（比如 "[Required]" / "[Optional]"）
```

Usage方法实现示例如下所示：

```go
func (r *Root) Usage() string {
    return `
    Root option:
      ${Proxy}  $[Proxy]  $<Proxy>
    
    Root Example:
      -proxy ""
    `
}
```

库将根据config中所配置的ArgStyle填充各Usage所返回的字符串模板并拼接所有层的字符串模板。

## 对外接口

### func Register(parentStructPtr interface{}, parentFieldName string, optStructPtr interface{})

使用该函数用以注册带有参数tag的结构体，**注册时没有顺序要求**。

> 参数含义

**parentStructPtr**：父结构体指针
**parentFieldName**：子结构体所关联的父结构体中的成员名称
**optStructPtr**：子结构体指针

> 使用示例

```go
func init() {
	argStruct.Register(&Root{}, "Proxy", &Sub1{})
    // 注册时没有顺序要求
    argStruct.Register(nil, "", &Root{})
    argStruct.Register(&Sub1{}, "Username", &Sub2{})
    argStruct.Register(&Sub2{}, "Password", &Sub3{})
}
```

### func GetOption\[T any\](config \*ParserConfig) (\*T, error)

使用该函数获取从不同Source中反射填充的结构体实例指针。

> 参数含义

**config**：配置结构体，其具体结构如下所示：

```go
type ParserConfig struct {
    // 参数风格,目前支持的风格有: "-"开头为键, StyleShortDash, 默认风格; "--"开头为键, StyleLongDash; 没有特殊开头, 以配置为准作为键, 类似于kubectl命令风格, StyleBare; 混合风格(暂未支持), StyleMixed
	ArgsStyle         Style
    // 参数源, 目前支持的源有: 主函数输入, 也就是os.args[], SrcCLI, 默认源; 来自环境变量(暂未支持), SrcEnv; 配置文件, SrcConfig(暂未支持)
	ArgsSource        Source
    // 布尔聚合, 该配置现暂未支持, 日后支持后将允许-vvv这样的参数或允许将-a -b -c输入为-abc
	BoolRepeatAsInc   bool
    // true, 仅校验T节点; false, 从T的root开始进行校验, 直到T为止, 校验范围[root, T]
	ValidateTOnly bool
}
```

> 使用示例

获取中间节点

```go
func main() {
    sub, err := argStruc.GetOption[Sub1](nil)
    if err != nil {
        panic(err)
    }
    ...
}
```

获取根节点

```go
func main() {
    root, err := argStruc.GetOption[Root](nil)
    if err != nil {
        panic(err)
    }
    ...
}
```

