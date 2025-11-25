# argStruct

## 描述

这是一个使用结构体驱动的、通过成员tag便可以实现简易配置的、基于Go语言的、Cli命令解析库

## 特点

- 以 **Go 结构体 + tag** 驱动 CLI 参数解析
- 支持多层级结构体，通过 `Register` 显式声明父子关系
- 支持 `required` + 自定义 `Validate(ctx)`，上下文感知校验
- 内置帮助信息渲染：Usage 模板 + 自动注入 `-h/--help`
- 零第三方依赖，纯标准库

## 安装

```powershell
go get github.com/ROOKIEMIE/argStruct@latest
```

## 快速上手

```go
type Global struct {
    Proxy string `alias:"proxy" desc:"Proxy url or host" required:""`
}

func init() {
    argStruct.Register(nil, "", &Global{})
}

func main() {
    g, err := argStruct.GetOption[Global](nil)
    if err != nil {
        if errors.Is(err, argStruct.ErrHelp) {
            os.Exit(0)
        }
        log.Fatal(err)
    }
    fmt.Println("Proxy:", g.Proxy)
}
```

配置运行参数：

```powershell
go run . -proxy http://127.0.0.1:8080
go run . -h
```

## 参数定义

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

## 对外函数Register

- `Register(nil, "", &Root{})` 声明 Root 为一个 root group；
- `Register(&Root{}, "Proxy", &Sub1{})` 声明 Root.Proxy 挂载 Sub1；
- **注册顺序不敏感**

### 完整函数签名

```go
func Register(parentStructPtr interface{}, parentFieldName string, optStructPtr interface{})
```

使用该函数用以注册带有参数tag的结构体，**注册时没有顺序要求**。

### 参数含义

**parentStructPtr**：父结构体指针
**parentFieldName**：子结构体所关联的父结构体中的成员名称
**optStructPtr**：子结构体指针

### 使用示例

```go
func init() {
	argStruct.Register(&Root{}, "Proxy", &Sub1{})
    // 注册时没有顺序要求
    argStruct.Register(nil, "", &Root{})
    argStruct.Register(&Sub1{}, "Username", &Sub2{})
    argStruct.Register(&Sub2{}, "Password", &Sub3{})
}
```

## Tag定义

| 名称        | 类型   | 是否必须 | 作用                                                         | 示例                                |
| ----------- | ------ | -------- | ------------------------------------------------------------ | ----------------------------------- |
| alias       | string | ✔        | 声明 flag 名，可以多个，逗号分隔，支持空格                   | alias:"p,proxy"                     |
| desc        | string | ✖        | 短描述，若同时有 `description`，则被覆盖                     | desc:"Proxy url or host"            |
| description | string | ✖        | 完整描述，优先级高于 `desc`                                  | description:"Full description here" |
| required    | 任意   | ✖        | 配置该参数是否为必要参数，这将影响该参数在后续自动校验时的结果，只要存在 `required` tag 就视为“必填”，tag 的取值内容会被忽略 | required:""                         |
| default     | string | ✖        | 默认值，按字段类型转换（int/float/bool/string 等）           | default:"8080"                      |

## 运行时解析配置ParserConfig

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
    // false, 开启Auto Help注入; true, 禁用Auto Help注入, 使用手动的方式管理Usage的输出
    DisableAutoHelp bool
}
```

### 额外说明：

- **ArgsStyle**：以下给出 `StyleShortDash / StyleLongDash / StyleBare` 的简要例子。

```powershell
# StyleShortDash
app.exe -arg1 v1 -arg2 v2
# StyleLongDash
app.exe --arg1 v1 --arg2 v2
# StyleBare
app.exe arg1 v1 arg2 v2
```

- **ValidateTOnly**：

现假设参数配置后的结构如下所示：

```txt
Root:
	- Proxy <==> Sub1:
					 - Username <==> Sub2:
					 					 - Password <==> Sub3:
					 					 					 - DeviceId
					 					 - Internal
					 - Count
```

当**ValidateTOnly**为**true**时，**仅会**进行GetOption所传入的T自身的校验；当**ValidateTOnly**为**false**时，将会从T的root参数结构体开始进行校验。无论进行哪种校验路径，都将**先**进行required **tag校验**，**再**进行Validate方法的**自定义校验**。

以下给出一个简图以便于你理解该过程：

```powershell
# ValidateTOnly = false
GetOption[Sub2](nil)
# Validator
Root <== Check Required Config
Root.Validate(ctx)
	Sub1 <== Check Required Config
	Sub1.Validate(ctx)
		Sub2 <== Check Required Config
		Sub2.Validate(ctx)
		# If not error
		return *Sub2{}

# ValidateTOnly = true
GetOption[Sub2](&argStruct.ParserConfig{ValidateTOnly: true})
# Validator
Sub2 <== Check Required Config
Sub2.Validate(ctx)
# If not error
return *Sub2{}
```

- **DisableAutoHelp / ErrHelp**：
  - 默认会自动检测 `-h/--help`，打印**全局** Usage，返回 `ErrHelp`；
  - 若想自己处理 help，可设置 `DisableAutoHelp: true` 并手动调用 `argStruct.Usage(config)`。

## 参数校验

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

### 使用示例

```go
func (sub3 *Sub3) Validate(ctx *ValidationContext) error {
    // 通过该对外函数可以获取当前节点(在该例子中就是Sub3)的任意前置节点(参数结构体)的实例
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

### 校验规则

- **自动 required 校验**：所有带 `required` tag 的字段都会自动检查是否有值；
- **自定义 Validate(ctx)**：按 root→T 顺序调用，每层先 required 再 `Validate`；
- 将受到**运行时解析配置ParserConfig**中**ValidateTOnly字段**的影响。

## 帮助信息

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

对于配置于参数结构体的**Usage**方法**所返回的字符串**目前支持在其中插入模板，目前已有的模板参数如下所示：

| 模板写法 | 变量含义       | 模板作用                                                     |
| -------- | -------------- | ------------------------------------------------------------ |
| ${v}     | 结构体成员名称 | 显示该字段的“主 alias”或完整 flag 形式，具体显示时将受**运行时解析配置ParserConfig**中的**ArgsStyle**配置项影响。 |
| $[v]     | 结构体成员名称 | 显示该字段的描述（desc）。                                   |
| $\<v>    | 结构体成员名称 | 显示 REQUIRED/optional 标记（比如 "[Required]" / "[Optional]"）。 |
| $(v)     | 结构体名称     | 以表格的形式打印该参数结构体中的所有alias、required和desc，表格中的各列左对齐并在各列之间填充列分隔，**$() 必须独占一行才会被视为“表格占位符”**，比如 `$(Root)` 这一行会被整行替换成表格，但 `foo $(Root) bar` 不会触发表格渲染；**占位符前面的缩进会被保留**。 |

Usage方法实现示例如下所示：

```go
func (r *Root) Usage() string {
    return `
    Root option:
    $(Root)
    
    Root Example:
      ${Proxy} ""
    `
}
```

库将根据config中所配置的ArgStyle填充各Usage所返回的字符串模板并拼接所有层的字符串模板。

效果：

```powershell
    Root option:
    -arg1  [Required]  this is arg1 desc
    -arg2  [Required]  this is arg2 desc
    
    Root Example:
    -p/-proxy ""
```

## 对外函数GetOption

### 完整函数签名

```go
func GetOption[T any](config *ParserConfig) (*T, error)
```

使用该函数获取从不同Source（**暂未实现**）中反射填充的结构体实例指针，结构体实例的类型以T来指示。

获取行为将受到传入的**运行时解析配置ParserConfig**影响，具体参见**运行时解析配置ParserConfig**这节内容。

若是希望以默认的Souce为Cli、StyleShortDash参数风格、不开启BoolRepeatAsInc、不开启ValidateTOnly、自动注入-h/--help可以仅传入nil，示例如下所示：

```go
func main() {
    sub, err := argStruct.GetOption[Sub1](nil)
    if err != nil {
        if errors.Is(err, argStruct.ErrHelp) {
            // 捕获到自动注入的帮助信息打印, 说明调用时使用了-h/--help参数, 可以在这里做进一步的处理
        } else {
            panic(err)
        }
    }
    ...
}
```

以自定义配置解析Souce传入的参数：

```go
func main() {
    sub, err := argStruct.GetOption[Sub1](&argStruct.ParserConfig{ValidateTOnly: true})
    if err != nil {
        if errors.Is(err, argStruct.ErrHelp) {
            // 捕获到自动注入的帮助信息打印, 说明调用时使用了-h/--help参数, 可以在这里做进一步的处理
        } else {
            panic(err)
        }
    }
    ...
}
```

## 局限

- 当前仅支持 CLI source（`ArgsSource=SrcCLI`），Env/Config 还在计划中；
- 暂不支持子命令（`app cmd ...`）和位置参数；
- 布尔累加（`-vvv` 等）尚未实现；
- ParserConfig 中某些字段是预留；
- 暂时仅支持基础类型解析（string、int（int、int8、int16、int32、int64）、uint（uint、uint8、uint16、uint32、uint64）、float（float32、float64）、bool），**暂不支持**容器类型解析（slice、map等）。
