package argStruct

import (
	"fmt"
	"os"
	"reflect"
	"strings"
)

type Style int

const (
	StyleShortDash Style = iota // "-a 1 -b 2"
	StyleLongDash               // "--addr 1 --port 2"
	StyleBare                   // "addr 1 port 2"
	StyleMixed                  // 预留：混合模式，将来支持
)

type Source int

const (
	SrcDefault Source = iota
	SrcConfig
	SrcEnv
	SrcCLI
)

type cliArgMap struct {
	argKey  string
	rawKey  string
	argVals []string
}

type ParserConfig struct {
	ArgsStyle       Style
	ArgsSource      Source
	BoolRepeatAsInc bool
	ValidateTOnly   bool
	DisableAutoHelp bool
}

type fieldState struct {
	template *option
	source   Source
	rawStr   string
	setValue any
	applyErr *argsErr
}

type runtime struct {
	config *ParserConfig

	rawCliArgs []string
	cliArgs    []*cliArgMap

	cliParseState []*fieldState
	stateByOpt    map[*option]*fieldState
	lastPath      []*optionGroup

	validationRoot any
	helpChecked    bool // 是否已经检查/处理过 auto-help

	err *argsErr
}

func newRuntime(args []string, config *ParserConfig) *runtime {
	return &runtime{
		config:     config,
		rawCliArgs: args,
		err:        &argsErr{funcName: "GetOption"},
	}
}

// handleAutoHelp 检测命令行中是否包含 -h/--help，并在需要时输出 Usage 并返回 ErrHelp。
// 注意：
//   - 仅在 ParserConfig.DisableAutoHelp == false 时由 GetOption 调用；
//   - 只检查 rawCliArgs，不依赖 aliasIndex 的解析结果；
//   - 如果用户已经将 "h"/"help" 作为 alias 使用，并且命令行中出现了对应的 -h/--help，会返回冲突错误。
func (r *runtime) handleAutoHelp(config *ParserConfig) error {
	if r == nil || config == nil {
		return nil
	}
	if r.helpChecked {
		// 避免同一个 runtime 被多次处理 help
		return nil
	}
	r.helpChecked = true

	// 1) 在原始 argv 中查找 -h/--help
	helpRequested := false
	for _, tok := range r.rawCliArgs {
		if tok == "-h" || tok == "--help" {
			helpRequested = true
			break
		}
	}
	if !helpRequested {
		return nil
	}

	// 2) 只有当用户实际传入了 -h/--help 时，才去考虑 alias 冲突问题。
	//    如果用户没传 -h/--help，即使 alias:"h"/"help" 也不算冲突。
	ix := globalSchema.ix
	if binds := ix.aliasIndex[aliasKey("h")]; len(binds) > 0 {
		return fmt.Errorf(
			"auto help flag \"-h\" conflicts with existing alias \"h\"; " +
				"please disable auto help by ParserConfig.DisableAutoHelp = true or change the alias",
		)
	}
	if binds := ix.aliasIndex[aliasKey("help")]; len(binds) > 0 {
		return fmt.Errorf(
			"auto help flag \"--help\" conflicts with existing alias \"help\"; " +
				"please disable auto help by ParserConfig.DisableAutoHelp = true or change the alias",
		)
	}

	// 3) 打印全局 Usage（这里选择全局帮助，而不是 T 子树帮助）
	if config.ArgsStyle == 0 {
		config.ArgsStyle = StyleShortDash
	}
	usage := globalSchema.usage(config.ArgsStyle)
	fmt.Fprint(os.Stdout, usage)

	// 返回一个可识别的 ErrHelp，调用方可以根据它决定是否退出
	return ErrHelp
}

func (r *runtime) scanShortDash() error {
	r.cliArgs = nil
	var cur *cliArgMap
	for _, tok := range r.rawCliArgs {
		if strings.HasPrefix(tok, "-") && !strings.HasPrefix(tok, "--") {
			key := strings.TrimPrefix(tok, "-")
			if key == "" {
				r.err.errMsg = "empty short flag '-' is not allowed"
				return r.err.ToErr()
			}

			// 关键：先看这个 key 是否是“已知 alias”
			if _, ok := globalSchema.ix.aliasIndex[aliasKey(key)]; !ok {
				// 未知 alias：尽量按 value 处理（允许负数、-foo 作为值）
				if cur == nil {
					r.err.errMsg = fmt.Sprintf("value %q appears before any flag", tok)
					return r.err.ToErr()
				}
				cur.argVals = append(cur.argVals, tok)
				continue
			}

			cur = &cliArgMap{
				argKey: key,
				rawKey: tok,
			}
			r.cliArgs = append(r.cliArgs, cur)
		} else {
			if cur == nil {
				r.err.errMsg = fmt.Sprintf("value %q appears before any flag", tok)
				return r.err.ToErr()
			}
			cur.argVals = append(cur.argVals, tok)
		}
	}
	return nil
}

func (r *runtime) scanLongDash() error {
	r.cliArgs = nil
	var cur *cliArgMap
	for _, tok := range r.rawCliArgs {
		if strings.HasPrefix(tok, "--") {
			key := strings.TrimPrefix(tok, "--")
			if key == "" {
				r.err.errMsg = "empty long flag '--' is not allowed"
				return r.err.ToErr()
			}
			cur = &cliArgMap{argKey: key, rawKey: tok}
			r.cliArgs = append(r.cliArgs, cur)
		} else {
			if cur == nil {
				r.err.errMsg = fmt.Sprintf("value %q appears before any flag", tok)
				return r.err.ToErr()
			}
			cur.argVals = append(cur.argVals, tok)
		}
	}
	return nil
}

func (r *runtime) scanBare() error {
	r.cliArgs = nil
	var cur *cliArgMap
	for _, tok := range r.rawCliArgs {
		if _, ok := globalSchema.ix.aliasIndex[aliasKey(tok)]; ok {
			// 已知 alias：开启新的 key
			cur = &cliArgMap{
				argKey: tok,
				rawKey: tok,
			}
			r.cliArgs = append(r.cliArgs, cur)
			continue
		}
		// 否则当作 value
		if cur == nil {
			r.err.errMsg = fmt.Sprintf("value %q appears before any option name (bare style)", tok)
			return r.err.ToErr()
		}
		cur.argVals = append(cur.argVals, tok)
	}
	return nil
}

func (r *runtime) scanMixed() error {
	return nil
}

// 将rawCliArgs提取为cliArgs
func (r *runtime) scanCliArgs() error {
	r.err.process = "Scan Cli Args"
	if r.config == nil {
		r.config = &ParserConfig{ArgsStyle: StyleShortDash}
	}
	switch r.config.ArgsStyle {
	case StyleShortDash:
		return r.scanShortDash()
	case StyleLongDash:
		return r.scanLongDash()
	case StyleBare:
		return r.scanBare()
	case StyleMixed:
		return r.scanMixed() // 以后再实现
	default:
		r.err.errMsg = fmt.Sprintf("unknown ArgsStyle %d", r.config.ArgsStyle)
		return r.err.ToErr()
	}
}

func (r *runtime) buildFieldStates(rootGroup *optionGroup) {
	var states []*fieldState
	byOpt := make(map[*option]*fieldState)

	var dfs func(g *optionGroup)
	dfs = func(g *optionGroup) {
		for _, opt := range g.fieldToOptions {
			fs := &fieldState{
				template: opt,
				source:   SrcDefault, // 先认为全是 default 来源
			}
			if opt.defaultStr != "" {
				// Register 阶段已经把 defaultStr 转成 defaultVal
				fs.setValue = opt.defaultVal
			}
			states = append(states, fs)
			byOpt[opt] = fs
		}
		for _, child := range globalSchema.ix.groupChildren[g] {
			dfs(child)
		}
	}

	dfs(rootGroup)
	r.cliParseState = states
	r.stateByOpt = byOpt
}

// applyGroupValues
// 该函数主要用于根据 optionGroup 构造 v，当该 optionGroup 存在子时，需要递归构建其所有子。
// v：当前 group 对应的 *struct；第一次调用时是 root（*Root 或 *T），
//
//	对子 group 的调用则由本函数自己负责创建实例，不再依赖父结构体字段类型。
func (r *runtime) applyGroupValues(v reflect.Value, g *optionGroup) error {
	r.err.process = "Apply Group Values"

	// 1. 保证 v 是 *g.structType。
	//    - 对于第一次调用（root）：从外面传进来的 new(T) 或 reflect.New(rootType)
	//    - 对于子 group：我们会传一个 invalid 的 reflect.Value 进来，这里会走 new()
	if !v.IsValid() {
		// 没有传实例进来，为当前 group 创建一个新的实例
		v = reflect.New(g.structType)
	} else {
		// 传进来的值存在：要求是指向同一类型的指针，否则认为是内部错误
		if v.Kind() != reflect.Ptr || v.Elem().Type() != g.structType {
			r.err.errMsg = fmt.Sprintf(
				"applyGroupValues: expect *%s for group %s, got %v",
				g.structType, g.structName, v.Type(),
			)
			return r.err.ToErr()
		}
	}

	// 1.1 无论如何，把“本次解析的实例指针”缓存到 group.structPtr
	// 后面 extractTargetFromRoot 以及 Validate 都应该通过 optionGroup.structPtr 拿实例，
	// 而不是再去猜测父结构体字段类型。
	g.structPtr = v.Interface()

	sv := v.Elem() // struct 本体

	// 2. 遍历该 group 下所有“字段级 option”
	for fieldName, opt := range g.fieldToOptions {
		// 2.1 如果这个字段挂载了子 group，则递归构建子 group 的实例。
		//     注意：这里不再依赖父 struct 上存在某个 *SubStruct / SubStruct 字段。
		if child := opt.subStructPtr; child != nil {
			// 这里直接传一个 invalid 的 reflect.Value 进去，让子 group 自己 new 实例并写入 child.structPtr。
			var childV reflect.Value
			if err := r.applyGroupValues(childV, child); err != nil {
				return err
			}
			// 父 struct 与子 struct 之间在数据上不做任何绑定，
			// 二者的“父子关系”完全通过 optionGroup 树和 ValidationContext 体现。
		}

		// 2.2 处理当前字段自身的值（例如 Global1.Proxy、Global1.ModelType 等）。
		fv := sv.FieldByName(fieldName)
		if !fv.IsValid() {
			// 理论上不该发生，说明 schema 和实际 struct 脱节了
			r.err.errMsg = fmt.Sprintf(
				"applyGroupValues: field %s not found in struct %s",
				fieldName, sv.Type().Name(),
			)
			return r.err.ToErr()
		}

		fs := r.stateByOpt[opt]
		if fs == nil || fs.setValue == nil {
			// 没有任何值（CLI / Env / Config / Default）需要写，跳过
			continue
		}
		if fs.applyErr == nil {
			fs.applyErr = &argsErr{
				process:  fmt.Sprintf("Set Arg[%s] Reflect Value", fs.rawStr),
				funcName: "runtime.applyGroupValues",
			}
		}

		// 2.3 这个字段必须是可写的（导出字段 + 可寻址）
		if !fv.CanSet() {
			fs.applyErr.errMsg = fmt.Sprintf(
				"applyGroupValues: field %s.%s is not settable (maybe unexported)",
				g.structName, fieldName,
			)
			return fs.applyErr.ToErr()
		}

		// 2.4 类型检查与必要的 Convert
		val := reflect.ValueOf(fs.setValue)
		if !val.Type().AssignableTo(fv.Type()) {
			if val.Type().ConvertibleTo(fv.Type()) {
				val = val.Convert(fv.Type())
			} else {
				fs.applyErr.errMsg = fmt.Sprintf(
					"applyGroupValues: cannot assign value of type %s to field %s.%s of type %s",
					val.Type(), g.structName, fieldName, fv.Type(),
				)
				return fs.applyErr.ToErr()
			}
		}

		// 2.5 真正写回当前 group 的字段
		fv.Set(val)
	}

	return nil
}

func (r *runtime) bindCliArgsToStates(result any) error {
	r.err.process = "Bind Cli Args"
	for _, cliArg := range r.cliArgs {
		binds, exist := globalSchema.ix.aliasIndex[aliasKey(cliArg.argKey)]
		if !exist {
			// 完全未知的 flag
			r.err.errMsg = fmt.Sprintf("unknown flag %q", cliArg.rawKey)
			return r.err.ToErr()
		}

		// 在当前 root 子树中找到和这个 alias 关联的 fieldState
		var matchedStates []*fieldState
		for _, b := range binds {
			fs, ok := r.stateByOpt[b.opt]
			if !ok {
				// alias 存在，但这个 option 不在本次 GetOption 的子树里
				continue
			}
			matchedStates = append(matchedStates, fs)
		}

		if len(matchedStates) == 0 {
			// alias 存在但不属于当前 rootGroup
			r.err.errMsg = fmt.Sprintf("flag %q does not belong to %T", cliArg.rawKey, result)
			return r.err.ToErr()
		}
		if len(matchedStates) > 1 {
			// 存在多重绑定歧义
			r.err.errMsg = fmt.Sprintf(
				"flag %q is ambiguous for %T; use full path alias like %q",
				cliArg.rawKey, result, binds[0].fieldPath,
			)
			return r.err.ToErr()
		}

		fs := matchedStates[0]
		fs.source = SrcCLI
		// 根据 argVals 决定 rawStr / setValue
		switch len(cliArg.argVals) {
		case 0:
			// 没有显式 value：只允许 bool 省略值
			t := fs.template.optionFieldType
			if t.Kind() != reflect.Bool {
				r.err.errMsg = fmt.Sprintf("option %q requires value, but none provided", cliArg.rawKey)
				return r.err.ToErr()
			}
			fs.rawStr = "true"
			v, err := transform(fs.rawStr, t)
			if err != nil {
				r.err.errMsg = err.Error()
				return r.err.ToErr()
			}
			fs.setValue = v
		case 1:
			fs.rawStr = cliArg.argVals[0]
			v, err := transform(fs.rawStr, fs.template.optionFieldType)
			if err != nil {
				r.err.errMsg = err.Error()
				return r.err.ToErr()
			}
			fs.setValue = v
		default:
			// 未来留给 slice / map 等多值类型
			r.err.errMsg = "current unsupported multi values"
			return r.err.ToErr()
		}
	}

	return nil
}

// 从目标 group 一直往上走 groupParents，找到整棵树的 root 以及 root -> ... -> target 的路径
func (r *runtime) findRootAndPath(target *optionGroup) (*optionGroup, []*optionGroup) {
	if target == nil {
		return nil, nil
	}
	ix := globalSchema.ix

	// 先从 target 一直往上收集链：target -> parent -> ... -> root
	var chain []*optionGroup
	for g := target; g != nil; {
		chain = append(chain, g)

		parent, ok := ix.groupParents[g]
		if !ok || parent == nil {
			break
		}
		g = parent
	}

	// chain 最后一个就是 root
	root := chain[len(chain)-1]

	// 反转一下得到 root -> ... -> target 的路径
	path := make([]*optionGroup, len(chain))
	for i := range chain {
		path[i] = chain[len(chain)-1-i]
	}
	return root, path
}

// 根据 T 的类型，在 schema 中找到唯一的 optionGroup
func resolveTargetGroup[T any](r *runtime) (*optionGroup, error) {
	r.err.process = "Resolve Target Group"

	// 拿到 T 的类型（要求 T 是 struct，而不是 *struct）
	var zero *T
	t := reflect.TypeOf(zero).Elem()
	if t.Kind() != reflect.Struct {
		r.err.errMsg = fmt.Sprintf("T must be struct, got %v", t)
		return nil, r.err.ToErr()
	}

	groups := globalSchema.findGroupsByType(t)
	if len(groups) == 0 {
		r.err.errMsg = fmt.Sprintf("resolveTargetGroup: no optionGroup schema found for type %s", t.Name())
		return nil, r.err.ToErr()
	}
	if len(groups) > 1 {
		r.err.errMsg = fmt.Sprintf("resolveTargetGroup:multiple optionGroups found for type %s", t.Name())
		return nil, r.err.ToErr()
	}
	return groups[0], nil
}

// rootPtr: *RootStructType
// path:    [rootGroup, ..., targetGroup]
func extractTargetFromRoot[T any](r *runtime, path []*optionGroup) (*T, error) {
	r.err.process = "Extract Target From Root"

	if len(path) == 0 {
		r.err.errMsg = "empty group path when extracting target"
		return nil, r.err.ToErr()
	}

	// 最后一个就是目标 group
	targetGroup := path[len(path)-1]

	// 1) 类型检查：确保 schema 里这个 group 的类型就是 T
	var zero *T
	t := reflect.TypeOf(zero).Elem()
	if targetGroup.structType != t {
		r.err.errMsg = fmt.Sprintf(
			"extractTargetFromRoot: type mismatch, group %s has type %v, but T is %v",
			targetGroup.structName, targetGroup.structType, t,
		)
		return nil, r.err.ToErr()
	}

	// 2) 确保有实例指针
	if targetGroup.structPtr == nil {
		r.err.errMsg = fmt.Sprintf(
			"extractTargetFromRoot: structPtr of group %s is nil; maybe applyGroupValues has not initialized it",
			targetGroup.structName,
		)
		return nil, r.err.ToErr()
	}

	v := reflect.ValueOf(targetGroup.structPtr)
	expectPtrType := reflect.TypeOf((*T)(nil)) // *T
	if !v.Type().AssignableTo(expectPtrType) {
		r.err.errMsg = fmt.Sprintf(
			"extractTargetFromRoot: internal type mismatch, expect %v, got %v",
			expectPtrType, v.Type(),
		)
		return nil, r.err.ToErr()
	}

	// v.Interface() 底层就是 *T
	return v.Interface().(*T), nil
}

/*applyCliArgs
 * 需要完成的几个操作:
 *	1. 通过 T 的类型找到对应的 rootGroup
 *  2. r.buildFieldStates(rootGroup)
 *	3. 用 aliasIndex 把 cliArgs 绑定到 fieldState.rawStr
 *	4. 对每个 fieldState 决定最终“来源”（Source）和 rawStr（CLI / Default / Env / Config）
 *	5. 调 transform 填 fieldState.setValue
 *	6. 用 reflect 把 setValue 写到 target 上
 */
// 入口：根据 ParserConfig 决定是否填充祖先
func applyCliArgs[T any](r *runtime) (*T, error) {
	r.err.process = "Apply Cli Args"

	// 1. 先找到“目标” group：T 对应的 optionGroup
	targetGroup, err := resolveTargetGroup[T](r)
	if err != nil {
		return nil, err
	}

	// 2. 永远从真正的 rootGroup 开始构建，并得到 root -> ... -> target 的路径
	rootGroup, path := r.findRootAndPath(targetGroup)
	if rootGroup == nil {
		r.err.errMsg = "internal error: cannot find rootGroup for target"
		return nil, r.err.ToErr()
	}

	// 3. 基于 rootGroup 构建 fieldState（包含 defaultVal）
	r.buildFieldStates(rootGroup)

	// rootPtr: *RootStructType
	rootPtr := reflect.New(rootGroup.structType)

	// CLI -> fieldState
	if err = r.bindCliArgsToStates(rootPtr.Interface()); err != nil {
		return nil, err
	}

	// fieldState -> 实际 root struct
	if err = r.applyGroupValues(rootPtr, rootGroup); err != nil {
		return nil, err
	}
	r.validationRoot = rootPtr.Interface()

	result, err := extractTargetFromRoot[T](r, path)
	if err != nil {
		return nil, err
	}
	r.lastPath = path

	return result, nil
}

func cache(config *ParserConfig) (*runtime, error) {
	cacheMutex.RLock()
	rt := cliCache[config.ArgsSource]
	if rt != nil && rt.config.ArgsSource == config.ArgsSource && rt.config.ArgsStyle == config.ArgsStyle {
		cacheMutex.RUnlock()
		return rt, nil
	}
	cacheMutex.RUnlock()

	rt = newRuntime(os.Args[1:], config)
	// 2) 自动帮助：在真正解析字段之前，先检测 -h/--help
	if !config.DisableAutoHelp {
		if err := rt.handleAutoHelp(config); err != nil {
			// 此处 ErrHelp 也会被返回，由调用方决定是否退出
			return nil, err
		}
	}

	// 真正新建
	switch config.ArgsSource {
	case SrcCLI:
		if err := rt.scanCliArgs(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported ArgsSource %d", config.ArgsSource)
	}

	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	if exist := cliCache[config.ArgsSource]; exist != nil &&
		exist.config.ArgsSource == config.ArgsSource &&
		exist.config.ArgsStyle == config.ArgsStyle {
		return exist, nil
	}
	cliCache[config.ArgsSource] = rt
	return rt, nil
}

func GetOption[T any](config *ParserConfig) (*T, error) {
	// 1) 规范化 config
	if config == nil {
		config = &ParserConfig{}
	}
	// 默认从 CLI 读
	if config.ArgsSource == 0 {
		config.ArgsSource = SrcCLI
	}
	// 默认 ArgsStyle
	if config.ArgsStyle == 0 {
		config.ArgsStyle = StyleShortDash
	}

	var (
		rt  *runtime
		err error
	)

	rt, err = cache(config)
	if err != nil {
		return nil, err
	}

	// 2) 把 runtime 中的状态应用到 T / 整棵树上
	result, err := applyCliArgs[T](rt)
	if err != nil {
		return nil, err
	}
	rt.config = config

	// 统一构造 ctx 链（Root→…→T）
	path, ctxs, err := globalSchema.buildValidationContext(rt.lastPath)
	if err != nil {
		return nil, err
	}

	// 3) 决定校验入口：
	//    - ValidateTOnly == false：若 validationRoot 存在，从 Root 开始校验（推荐默认）
	//    - ValidateTOnly == true：只从 T 开始校验
	if config.ValidateTOnly {
		// 只校验 T，但 T.Validate(ctx) 看到完整祖先链
		if err = globalSchema.targetValidate(path, ctxs); err != nil {
			return nil, err
		}
	} else {
		// 从 Root→…→T 整条路径逐层做 required + Validate(ctx)
		if err = globalSchema.rootValidate(path, ctxs); err != nil {
			return nil, err
		}
	}

	return result, nil
}
