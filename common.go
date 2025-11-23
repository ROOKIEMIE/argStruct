package argStruct

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const errFormatStr = "[%v](%v) %s"

type argsErr struct {
	funcName string
	process  string
	errMsg   string
}

func (e *argsErr) ToString() string {
	return fmt.Sprintf(errFormatStr, e.funcName, e.process, e.errMsg)
}

func (e *argsErr) ToErr() error {
	return errors.New(e.ToString())
}

// ErrHelp 表示用户请求了自动帮助信息（例如传入了 -h/--help）。
// GetOption 在触发自动帮助时会返回 ErrHelp，调用方可以根据它决定是否退出程序。
var ErrHelp = errors.New("argStruct: help requested")

const (
	AliasTagName            = "alias"
	DescriptionShortTagName = "desc"
	DescriptionFullTagName  = "description"
	RequiredTagName         = "required"
	DefaultTagName          = "default"
)

var (
	usageNameRe = regexp.MustCompile(`\$\{([A-Za-z0-9_.]+)\}`)
	usageDescRe = regexp.MustCompile(`\$\[([A-Za-z0-9_.]+)\]`)
	usageReqRe  = regexp.MustCompile(`\$<([A-Za-z0-9_.]+)>`)

	// 整行 $(Group) / $(Global1.Sub1) → 被视为“插入表格”的占位符
	// 分组 1 捕获前导空白，用于缩进；分组 2 捕获 group token。
	usageTableRe = regexp.MustCompile(`^(\s*)\$\(([A-Za-z0-9_.]+)\)\s*$`)
)

func transform(val string, t reflect.Type) (interface{}, error) {
	switch t.Kind() {
	case reflect.String:
		return val, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(val, 10, t.Bits())
		if err != nil {
			return nil, err
		}
		v := reflect.New(t).Elem()
		v.SetInt(i)
		return v.Interface(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u, err := strconv.ParseUint(val, 10, t.Bits())
		if err != nil {
			return nil, err
		}
		v := reflect.New(t).Elem()
		v.SetUint(u)
		return v.Interface(), nil
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(val, t.Bits())
		if err != nil {
			return nil, err
		}
		v := reflect.New(t).Elem()
		v.SetFloat(f)
		return v.Interface(), nil
	case reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return nil, err
		}
		return b, nil
	default:
		return nil, errors.New("unsupported type: " + t.String())
	}
}

type usageProvider interface {
	Usage() string
}

// ValidationContext 对外暴露的校验上下文
type ValidationContext struct {
	// ===== 下面这些字段全部小写，只给库内部用 =====
	root   any                // 整棵树的根实例（*Root）
	self   any                // 当前这一层的 struct 实例（*Sub1）
	parent *ValidationContext // 上一层的 context，形成链
	path   string             // 例如 "Root.Sub1"（来自 optionGroup.optionGroupPath）
}

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

type validateProvider interface {
	Validate(ctx *ValidationContext) error
}

type aliasKey string
type binding struct {
	groupPath string // e.g. "Global1.Sub1"
	fieldPath string // e.g. "Global1.Sub1.Username"
	group     *optionGroup
	opt       *option
}

type optionIndex struct {
	// 导航与消歧
	groupsByPath map[string]*optionGroup // "Global1", "Global1.Sub1"
	fieldsByPath map[string]*option      // "Global1.Sub1.Username"
	aliasIndex   map[aliasKey][]*binding // "u" -> [binding]; "Global1.Sub1.Username" -> [binding]

	// 父子边（派生出来，GetOptions 也可能需要）
	groupChildren map[*optionGroup][]*optionGroup
	groupParents  map[*optionGroup]*optionGroup

	// 根与主路径
	roots       []*optionGroup
	main        *optionGroup
	initialized bool
}

func newOptionIndex() *optionIndex {
	return &optionIndex{
		groupsByPath:  map[string]*optionGroup{},
		fieldsByPath:  map[string]*option{},
		aliasIndex:    map[aliasKey][]*binding{},
		groupChildren: map[*optionGroup][]*optionGroup{},
		groupParents:  map[*optionGroup]*optionGroup{},
	}
}

func (ix *optionIndex) reset() {
	ix.groupsByPath = make(map[string]*optionGroup)
	ix.fieldsByPath = make(map[string]*option)
	ix.aliasIndex = make(map[aliasKey][]*binding)
	ix.groupChildren = make(map[*optionGroup][]*optionGroup)
	ix.groupParents = make(map[*optionGroup]*optionGroup)
	ix.roots = nil
	ix.main = nil
	ix.initialized = false
}

// 从叶子 group 开始，一路往上收集到 root
// 返回顺序：leaf -> parent -> ... -> root
func (ix *optionIndex) groupChainUp(g *optionGroup) []*optionGroup {
	var chain []*optionGroup
	cur := g
	for cur != nil {
		chain = append(chain, cur)
		cur = ix.groupParents[cur]
	}
	return chain
}

func (ix *optionIndex) dfsBuild(optG *optionGroup, visited map[*optionGroup]bool) error {
	if optG == nil {
		return nil
	}
	if visited[optG] {
		return nil // 防止环，理论上树结构不会有，但多一层安全
	}
	visited[optG] = true

	// 1) groupPath
	gp := optG.optionGroupPath
	if gp == "" {
		gp = optG.structName
		optG.optionGroupPath = gp
	}
	if existG, exists := ix.groupsByPath[gp]; exists && existG != optG {
		globalSchema.err.process = "Build OptionIndex"
		globalSchema.err.errMsg = fmt.Sprintf(
			"group path %q already bound to %s, cannot bind to %s",
			gp, existG.structName, optG.structName,
		)
		return globalSchema.err.ToErr()
	}
	ix.groupsByPath[gp] = optG

	// 2) 遍历该 group 下的所有字段 option（以 field 为单位，不以 alias 为单位）
	for _, opt := range optG.fieldToOptions {
		// 2.1 计算 fieldPath
		fp := opt.optionPath
		if fp == "" {
			fp = gp + "." + opt.optionFieldName
			opt.optionPath = fp
		}

		// fieldPath 冲突检测
		if existOpt, exists := ix.fieldsByPath[fp]; exists && existOpt != opt {
			globalSchema.err.process = "Build OptionIndex"
			globalSchema.err.errMsg = fmt.Sprintf(
				"field path %q already bound to %s.%s, cannot bind to %s.%s",
				fp,
				existOpt.belongPtr.structName,
				existOpt.optionFieldName,
				opt.belongPtr.structName,
				opt.optionFieldName,
			)
			return globalSchema.err.ToErr()
		}
		ix.fieldsByPath[fp] = opt
		blind := &binding{
			groupPath: gp,
			fieldPath: fp,
			group:     optG,
			opt:       opt,
		}

		// 2.2 普通 alias -> 冲突检测 + 追加
		for _, a := range opt.alias {
			k := aliasKey(a)
			prev := ix.aliasIndex[k]

			// 同一个 group scope 内不允许同 alias 绑定不同 field
			for _, b := range prev {
				if b.groupPath == gp && b.fieldPath != fp {
					globalSchema.err.process = "Build OptionIndex"
					globalSchema.err.errMsg = fmt.Sprintf(
						"alias %q conflicts in group %q: %s and %s",
						a, gp, b.fieldPath, fp,
					)
					return globalSchema.err.ToErr()
				}
			}
			ix.aliasIndex[k] = append(prev, blind)
		}

		// 2.3 路径别名（显式消歧）：只要 fieldPath 不冲突即可
		pathKey := aliasKey(fp)
		ix.aliasIndex[pathKey] = append(ix.aliasIndex[pathKey], blind)

		// 2.4 处理子 group（通过 subStructPtr）
		if child := opt.subStructPtr; child != nil {
			// 父子关系索引
			ix.groupChildren[optG] = append(ix.groupChildren[optG], child)

			if oldParent, exist := ix.groupParents[child]; exist && oldParent != optG {
				// 这个错误理论上 mountChildren 已经兜底，这里再 double-check 一次。
				globalSchema.err.process = "Build OptionIndex"
				globalSchema.err.errMsg = fmt.Sprintf(
					"group %s already has parent %s, cannot add parent %s",
					child.structName,
					oldParent.structName,
					optG.structName,
				)
				return globalSchema.err.ToErr()
			}
			ix.groupParents[child] = optG

			// 继续 DFS
			if err := ix.dfsBuild(child, visited); err != nil {
				return err
			}
		}
	}
	return nil
}

// 计算从 root 出发的最大深度（以 group 层数为单位）
// 深度定义：单个节点的深度为 1，root → child 为 2，依此类推。
func (ix *optionIndex) treeDepth(root *optionGroup, visited map[*optionGroup]bool) int {
	if root == nil {
		return 0
	}
	// 防止意外成环（正常情况下是纯树，但这里多一层保护）
	if visited[root] {
		return 0
	}
	visited[root] = true

	maxChild := 0
	children := ix.groupChildren[root]
	for _, child := range children {
		d := ix.treeDepth(child, visited)
		if d > maxChild {
			maxChild = d
		}
	}

	// 对于纯树来说，这一行其实无所谓；保留是为了“路径级 visited”
	delete(visited, root)
	return maxChild + 1
}

// update 索引更新
func (ix *optionIndex) update(optionGroupTrees []*optionGroup) error {
	ix.reset()
	ix.roots = optionGroupTrees

	visited := make(map[*optionGroup]bool)
	for _, optG := range optionGroupTrees {
		optG.updateCurrentPath("")
		if err := ix.dfsBuild(optG, visited); err != nil {
			return err
		}
	}

	// root 不应该有 parent
	for _, root := range ix.roots {
		if p, ok := ix.groupParents[root]; ok && p != nil {
			globalSchema.err.process = "Build OptionIndex"
			globalSchema.err.errMsg = fmt.Sprintf(
				"root %s unexpectedly has parent %s (tree structure broken)",
				root.structName,
				p.structName,
			)
			return globalSchema.err.ToErr()
		}
	}

	// 选出“最高”的那棵树作为 main
	var (
		bestRoot  *optionGroup
		bestDepth int
	)
	for _, root := range ix.roots {
		d := ix.treeDepth(root, make(map[*optionGroup]bool))
		if d > bestDepth {
			bestDepth = d
			bestRoot = root
		}
	}
	if bestRoot != nil {
		ix.main = bestRoot
	}

	ix.initialized = true
	return nil
}

type schema struct {
	err *argsErr
	ix  *optionIndex
	// 所有参数树
	optionGroupTrees []*optionGroup
}

// 把 alias 列表格式化为命令行 flag 字符串
func formatAlias(opt *option, style Style) string {
	if len(opt.alias) == 0 {
		return opt.optionFieldName
	}

	var aliasWithStyle []string
	for _, a := range opt.alias {
		switch style {
		case StyleShortDash:
			aliasWithStyle = append(aliasWithStyle, "-"+a)
		case StyleLongDash:
			aliasWithStyle = append(aliasWithStyle, "--"+a)
		default:
			aliasWithStyle = append(aliasWithStyle, a)
		}
	}
	return strings.Join(aliasWithStyle, "/")
}

// findGroupForUsageTableToken 根据 $(token) 中的内容解析出对应的 optionGroup：
//
// 1. 若 token == 当前 group 的 structName 或 optionGroupPath，则直接用当前 group；
// 2. 若 token 含 "."，视为完整 groupPath，直接用 ix.groupsByPath 查找；
// 3. 否则按 structName 在所有 group 中搜索：
//   - 找到唯一一个匹配 structName 的 group → 使用它；
//   - 找不到或找到多个（歧义） → 返回 nil。
func (sc *schema) findGroupForUsageTableToken(token string, curGroup *optionGroup) *optionGroup {
	// 情况 1：命中当前 group 自己
	if curGroup != nil && (token == curGroup.structName || token == curGroup.optionGroupPath) {
		return curGroup
	}

	// 情况 2：带 "." 的路径形式，直接按 groupPath 查
	if strings.Contains(token, ".") {
		if g, ok := sc.ix.groupsByPath[token]; ok {
			return g
		}
		return nil
	}

	// 情况 3：按 structName 全局搜索
	var found *optionGroup
	for _, g := range sc.ix.groupsByPath {
		if g.structName == token {
			if found != nil {
				// 说明有多个同名 struct，歧义，返回 nil 保持模板原样
				return nil
			}
			found = g
		}
	}
	return found
}

func (sc *schema) resolveNameToken(curGroup *optionGroup, token string, style Style) string {
	var (
		targetGroup *optionGroup
		fieldName   string
	)

	if strings.Contains(token, ".") {
		// 路径形式
		lastDot := strings.LastIndex(token, ".")
		path := token[:lastDot]       // "Global1.Sub1"
		fieldName = token[lastDot+1:] // "Username"

		var ok bool
		targetGroup, ok = sc.ix.groupsByPath[path]
		if !ok {
			// 未找到，直接原样返回 token 或给一个占位提示
			return "${" + token + "}"
		}
	} else {
		// 简名形式，先在当前 group 找
		fieldName = token
		targetGroup = curGroup
	}

	opt, ok := targetGroup.fieldToOptions[fieldName]
	if !ok {
		return "${" + token + "}"
	}

	return formatAlias(opt, style)
}

func (sc *schema) resolveDescToken(g *optionGroup, token string) string {
	return g.fieldToOptions[token].desc
}

func (sc *schema) resolveRequiredToken(g *optionGroup, token string) string {
	if g.fieldToOptions[token].required {
		return "Required option"
	}
	return "Dispensable option"
}

/*
expandUsageTemplate

约定：
${Field} -> 显示该字段的“主 alias”或完整 flag 形式
$[Field] -> 显示该字段的描述（desc）
$<Field> -> 显示 REQUIRED/optional 标记（比如 "[Required]" / "[Optional]"）
*/
func (sc *schema) expandUsageTemplate(tpl string, g *optionGroup, style Style) string {
	// 1) ${...} -> alias（支持路径）
	tpl = usageNameRe.ReplaceAllStringFunc(tpl, func(m string) string {
		sub := usageNameRe.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		name := sub[1] // 捕获组 1
		return sc.resolveNameToken(g, name, style)
	})

	// 2) $[...] -> desc
	tpl = usageDescRe.ReplaceAllStringFunc(tpl, func(m string) string {
		sub := usageDescRe.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		name := sub[1]
		return sc.resolveDescToken(g, name)
	})

	// 3) $<...> -> [Required]/[Optional]
	tpl = usageReqRe.ReplaceAllStringFunc(tpl, func(m string) string {
		sub := usageReqRe.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		name := sub[1]
		return sc.resolveRequiredToken(g, name)
	})

	return tpl
}

func (sc *schema) writeDefaultGroupUsage(b *strings.Builder, g *optionGroup, style Style) {
	if g == nil {
		return
	}

	// 组名（比如 Sub1 / Global1）
	b.WriteString(g.structName)
	b.WriteByte('\n')

	// 使用一个固定缩进（比如 8 个空格，你可以根据自己的审美再调）
	const indent = 8
	tbl := g.toTable(style, indent)
	if tbl == nil {
		return
	}

	for _, line := range tbl.ToStrings() {
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

// writeGroupUsageBody 负责渲染“当前 group 自己”的 Usage 内容：
// - 如果实现了 Usage()，则使用模板 + ${}/$[]/$<> + $(...) 表格展开；
// - 否则走默认的表格风格（writeDefaultGroupUsage）。
// 注意：这里不处理前后多余空行，也不递归子 group。
func (sc *schema) writeGroupUsageBody(b *strings.Builder, g *optionGroup, style Style) {
	if g == nil {
		return
	}

	if up, ok := g.structPtr.(usageProvider); ok {
		// 用户写的模板
		raw := up.Usage()
		// 先展开 ${}/$[]/$<> 这三种占位符
		rendered := sc.expandUsageTemplate(raw, g, style)

		// 按行处理，支持整行 $(Group) 占位符
		lines := strings.Split(rendered, "\n")
		for _, line := range lines {
			// 不要在末尾多加一个空的换行（Split 会保留最后一个空串）
			if line == "" {
				b.WriteByte('\n')
				continue
			}

			// 先尝试匹配整行 $(...) 模板
			if sub := usageTableRe.FindStringSubmatch(line); len(sub) == 3 {
				indentStr := sub[1] // 捕获的前导空白
				token := sub[2]     // $(...) 里的内容

				// 解析 token 对应的 group
				tg := sc.findGroupForUsageTableToken(token, g)
				if tg == nil {
					// 找不到就原样输出这一行
					b.WriteString(line)
					b.WriteByte('\n')
					continue
				}

				// 计算缩进宽度，这里简单用 len(indentStr)，因为我们约定 indent 用空格或 Tab。
				indentWidth := len(indentStr)
				tbl := tg.toTable(style, indentWidth)
				if tbl == nil {
					// 防御性判断：没有表就原样输出
					b.WriteString(line)
					b.WriteByte('\n')
					continue
				}

				// 用表格内容替换整行 $(...)
				for _, rowLine := range tbl.ToStrings() {
					b.WriteString(rowLine)
					b.WriteByte('\n')
				}
				continue
			}

			// 普通行，直接输出
			b.WriteString(line)
			b.WriteByte('\n')
		}
	} else {
		// 没有自定义 Usage 时，生成一个默认块（类似 go-arg/kong 的风格）
		sc.writeDefaultGroupUsage(b, g, style)
	}
}

func (sc *schema) appendUsage(b *strings.Builder, g *optionGroup, depth int, style Style) {
	if g == nil {
		return
	}

	// 在每个 group 前加一个空行，做视觉分隔
	b.WriteByte('\n')

	// 渲染当前 group 自己的 Usage 内容
	sc.writeGroupUsageBody(b, g, style)

	// 在当前 group 后再加一个空行
	b.WriteByte('\n')

	// 递归子 group
	for _, child := range sc.ix.groupChildren[g] {
		sc.appendUsage(b, child, depth+1, style)
	}
}

func (sc *schema) usage(style Style) string {
	sc.err.process = "Usage"
	var b strings.Builder
	for _, root := range sc.ix.roots {
		sc.appendUsage(&b, root, 0, style)
	}
	return b.String()
}

// Usage 是对外的默认帮助入口，相当于“打印全部帮助信息”。
// 内部直接委托给 GetFullUsage，保持行为一致。
func Usage(config *ParserConfig) string {
	return GetFullUsage(config)
}

// GetFullUsage 获取全局所有已注册参数树的帮助信息。
// 现在是“全局帮助”的实现；后续如果要支持“按树选择”也可以在这里扩展。
func GetFullUsage(config *ParserConfig) string {
	// 规范化 config / ArgsStyle，与 GetOption 保持一致的默认行为
	if config == nil {
		config = &ParserConfig{}
	}
	if config.ArgsStyle == 0 {
		config.ArgsStyle = StyleShortDash
	}
	return globalSchema.usage(config.ArgsStyle)
}

// GetUsage 获取某个参数结构体 T 的帮助信息（包含其子树）。
// T 必须是 struct 类型（与 GetOption 的约束保持一致）。
//
// 语义：
//   - 只渲染 “T 对应的 optionGroup 以及它的子 group” 的帮助信息；
//   - 如果 T 在 schema 中没有注册，或者注册了多个 group，会 panic 提示。
func GetUsage[T any](config *ParserConfig) (string, error) {
	// 规范化 config / ArgsStyle
	if config == nil {
		config = &ParserConfig{}
	}
	if config.ArgsStyle == 0 {
		config.ArgsStyle = StyleShortDash
	}

	// 1) 拿到 T 的类型（要求 T 是 struct，而不是 *struct）
	var zero *T
	t := reflect.TypeOf(zero).Elem()
	if t.Kind() != reflect.Struct {
		return "", fmt.Errorf("argStruct.GetUsage: T must be struct, got %v", t)
	}

	// 2) 在 schema 中按类型查找唯一的 optionGroup
	groups := globalSchema.findGroupsByType(t)
	switch len(groups) {
	case 0:
		return "", fmt.Errorf("argStruct.GetUsage: no optionGroup schema found for type %s", t.Name())
	case 1:
		// ok
	default:
		return "", fmt.Errorf("argStruct.GetUsage: multiple optionGroups found for type %s", t.Name())
	}
	targetGroup := groups[0]

	// 3) 用和全局 Usage 同一套渲染逻辑，但只从 targetGroup 开始递归
	var buf strings.Builder
	globalSchema.appendUsage(&buf, targetGroup, 0, config.ArgsStyle)
	return buf.String(), nil
}

// 假设已经有 targetGroup 和 rootGroup（或者 path）
// 返回：pathGroups: [root, ..., target]
//
//	pathCtx:    与 pathGroups 一一对应的 ValidationContext
func (sc *schema) buildValidationContext(path []*optionGroup) ([]*optionGroup, []*ValidationContext, error) {
	sc.err.funcName = "GetOption"
	sc.err.process = "Build ValidationContext"
	if len(path) == 0 {
		sc.err.errMsg = "buildValidationContext: option group path is empty"
		return nil, nil, sc.err.ToErr()
	}

	// 整条路径共享同一个 root 实例
	rootGroup := path[0]
	rootInst := rootGroup.structPtr
	if rootInst == nil {
		sc.err.errMsg = "buildValidationContext: path root structPtr point to nil"
		return nil, nil, sc.err.ToErr()
	}

	var (
		ctxs      = make([]*ValidationContext, len(path))
		parentCtx *ValidationContext
	)

	for i, g := range path {
		inst := g.structPtr
		if inst == nil {
			sc.err.errMsg = fmt.Sprintf("buildValidationContext: path [%s] structPtr point to nil", g.structName)
			return nil, nil, sc.err.ToErr()
		}
		ctx := &ValidationContext{
			root:   rootInst,
			self:   inst,
			parent: parentCtx,
			path:   g.optionGroupPath, // 已经在 index.update 里填好了
		}
		ctxs[i] = ctx
		parentCtx = ctx
	}

	return path, ctxs, nil
}

// 对单个 group 做“浅校验”：只校验本 struct，不递归子 group。
func (sc *schema) validateGroupShallow(g *optionGroup, ctx *ValidationContext) error {
	structPtr := g.structPtr
	if structPtr == nil {
		sc.err.errMsg = fmt.Sprintf("validateGroupShallow: group %s has nil structPtr", g.structName)
		return sc.err.ToErr()
	}

	v := reflect.ValueOf(structPtr)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		sc.err.errMsg = fmt.Sprintf("validateGroupShallow: group %s expect *struct, got %T",
			g.structName, structPtr)
		return sc.err.ToErr()
	}
	sv := v.Elem()

	// 1) 自动 required 校验（当前 group 自己的字段）
	for fieldName, opt := range g.fieldToOptions {
		if !opt.required {
			continue
		}
		fv := sv.FieldByName(fieldName)
		if !fv.IsValid() {
			sc.err.errMsg = fmt.Sprintf("validateGroupShallow: required field %s.%s not found in value",
				g.structName, fieldName)
			return sc.err.ToErr()
		}
		if fv.IsZero() && opt.optionFieldType.Kind() != reflect.Bool {
			sc.err.errMsg = fmt.Sprintf("validateGroupShallow: required option %s.%s (%v) is missing",
				g.structName, fieldName, opt.alias)
			return sc.err.ToErr()
		}
	}

	// 2) struct 级 Validate(ctx)
	if vp, ok := structPtr.(validateProvider); ok {
		if err := vp.Validate(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (sc *schema) rootValidate(path []*optionGroup, ctxs []*ValidationContext) error {
	for i, g := range path {
		if err := sc.validateGroupShallow(g, ctxs[i]); err != nil {
			return err
		}
	}
	return nil
}

func (sc *schema) targetValidate(path []*optionGroup, ctxs []*ValidationContext) error {
	if len(path) == 0 {
		sc.err.errMsg = "targetValidate: option group path is empty"
		return sc.err.ToErr()
	}
	last := len(path) - 1
	g := path[last]
	ctx := ctxs[last]
	return sc.validateGroupShallow(g, ctx)
}

func (sc *schema) attachRootsAsChildrenOf(newParent *optionGroup) error {
	if newParent == nil {
		return nil
	}
	if len(sc.optionGroupTrees) == 0 {
		return nil
	}

	var (
		match    []*optionGroup
		newRoots []*optionGroup
	)

	for _, root := range sc.optionGroupTrees {
		// 不要把自己当成自己的 child
		if root == newParent {
			newRoots = append(newRoots, root)
			continue
		}

		if root.parentType == newParent.structType {
			// 这个 root 逻辑上应该挂到 newParent 底下
			match = append(match, root)
		} else {
			newRoots = append(newRoots, root)
		}
	}

	if len(match) > 0 {
		if err := newParent.mountChildren(match); err != nil {
			return err
		}
		// 被挂走的 child 不再作为 root
		sc.optionGroupTrees = newRoots
	}

	return nil
}

func (sc *schema) findGroupsByType(t reflect.Type) []*optionGroup {
	var res []*optionGroup

	var dfs func(g *optionGroup)
	dfs = func(g *optionGroup) {
		if g.structType == t {
			res = append(res, g)
		}
		for _, opt := range g.fieldToOptions {
			if opt.subStructPtr != nil {
				dfs(opt.subStructPtr)
			}
		}
	}

	for _, root := range sc.optionGroupTrees {
		dfs(root)
	}
	return res
}

/*addNewOptionGroup
 * 这其实应该是一个将一个节点插入到现有森林的一个问题,应该假设sc.optionGroupTrees都是完整的树
 * 那么当新节点被插入后,就有有几种情况
 * 1. 其没有父,那么它必是一个root,这就有可能:
 * 		1.1. 现有的树需要判断一下是否需要挂在它之下
 *		1.2. 它是否是当前多棵树的根?报错:同一个 child 不允许挂到多个 parent 上
 * 2. 其存在父,那么又存在:
 *		2.1. 它的父不存在,暂时将它作为root,成为一棵新的树
 *		2.2. 它的父存在,获取它的父,检查它的父是否已经存在子
 *				2.2.1. 若已存在,报错:同一个 child 不允许挂到多个 parent 上
 *				2.2.2. 若不存在,将它挂载到该父下
 *		2.3. 检测其是目前谁的父,这里沿用1.的逻辑
 * 完成上述过程实际上就完成了新optionGroup的挂载,因为每次Register调用都仅会插入一个optionGroup,因此不需要进行挪动大量节点
 * 还于sc.optionGroupTrees成环的检测,似乎应该留到GetOption中才做方才合适
 * 因为2.1.的存在,可能在中途出现所有节点都有父,但它们的父其实都还没注册的情况
 * 在GetOption中才可以保证所有节点都已经注册完毕了
 *
 * 有了新的sc.optionGroupTrees就可以更新每个节点的路径、选出最长的root
 */
func (sc *schema) addNewOptionGroup(newOptionGroup *optionGroup) error {
	if newOptionGroup == nil {
		sc.err.process = "AddNewOptionGroup"
		sc.err.errMsg = "new option group is nil"
		return sc.err.ToErr()
	}

	// 特殊情况：之前还没有任何树，直接作为 root
	if len(sc.optionGroupTrees) == 0 {
		sc.optionGroupTrees = append(sc.optionGroupTrees, newOptionGroup)
		return nil
	}

	// 1. 先解决“我是谁的子”
	if newOptionGroup.parentType != nil {
		// 在整个森林中查找 parentType 对应的 group
		match := sc.findGroupsByType(newOptionGroup.parentType)
		if len(match) > 1 {
			// 同一个 structType 找到了多个 parent，说明树结构本身就有歧义
			sc.err.process = "Build Tree"
			sc.err.errMsg = fmt.Sprintf(
				"multiple parents of type %s found for child %s (field %s)",
				newOptionGroup.parentType.Name(),
				newOptionGroup.structName,
				newOptionGroup.parentFieldName,
			)
			return sc.err.ToErr()
		} else if len(match) == 1 {
			parent := match[0]
			// 挂到 parent 指定的字段下
			if err := parent.mountChildren([]*optionGroup{newOptionGroup}); err != nil {
				return err
			}
			// 注意：这种情况下 newOptionGroup 已经在树里，不应该作为 root 加入 optionGroupTrees
		} else {
			// 父目前还没注册，只能先作为新的 root
			sc.optionGroupTrees = append(sc.optionGroupTrees, newOptionGroup)
		}
	} else {
		// 没有 parentType，天生是 root
		sc.optionGroupTrees = append(sc.optionGroupTrees, newOptionGroup)
	}

	// 2. 再解决“谁是我的子”
	//   现在森林里所有 root 中，凡是 parentType == newOptionGroup.structType 的，
	//   都应该挂到 newOptionGroup 下。
	if err := sc.attachRootsAsChildrenOf(newOptionGroup); err != nil {
		return err
	}

	return nil
}

func (sc *schema) storeOptionGroup(newOptionGroup *optionGroup) error {
	if newOptionGroup == nil {
		globalSchema.err.process = "Store Option Group"
		globalSchema.err.errMsg = "new option group is nil"
		return globalSchema.err.ToErr()
	}

	if err := sc.addNewOptionGroup(newOptionGroup); err != nil {
		return err
	}

	if err := sc.ix.update(sc.optionGroupTrees); err != nil {
		return err
	}

	return nil
}

var (
	registerMutex sync.Mutex
	cacheMutex    sync.RWMutex
	cliCache      = make(map[Source]*runtime)
	globalSchema  = &schema{ix: newOptionIndex(), err: &argsErr{}}
)
