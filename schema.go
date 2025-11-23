package argStruct

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// 支持带空格也支持不带空格: alias:"mt,model-type" or alias:"mt, model-type"
func extractAlias(aliases string) []string {
	parts := strings.Split(aliases, ",")
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		a := strings.TrimSpace(p)
		if a == "" {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}

func hasField(opt any, optName string) (bool, error) {
	if opt == nil {
		return false, errors.New("opt is nil")
	}
	if optName == "" {
		return false, errors.New("optName is empty")
	}

	t := reflect.TypeOf(opt)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false, errors.New("opt must be a struct or *struct")
	}

	if _, ok := t.FieldByName(optName); ok {
		return true, nil
	}
	return false, nil
}

type option struct {
	optionPath string

	// tag info
	alias      []string
	desc       string
	required   bool
	defaultStr string
	defaultVal any

	// field info
	belongPtr       *optionGroup
	optionFieldPtr  interface{}
	optionFieldType reflect.Type
	optionFieldName string
	subStructPtr    *optionGroup
}

func (opt *option) reflectFillFieldTag() error {
	globalSchema.err.process = "Reflect Fill Field Tag"
	if opt.optionFieldPtr == nil {
		globalSchema.err.errMsg = "option field prt is nil"
		return globalSchema.err.ToErr()
	}
	if opt.optionFieldType == nil {
		globalSchema.err.errMsg = "option field type is nil"
		return globalSchema.err.ToErr()
	}
	fieldTag, assert := opt.optionFieldPtr.(*reflect.StructField)
	if !assert {
		globalSchema.err.errMsg = "assert to *reflect.StructField fail"
		return globalSchema.err.ToErr()
	}
	if aliases, exist := fieldTag.Tag.Lookup(AliasTagName); exist {
		aliasList := extractAlias(aliases)
		if len(aliasList) <= 0 {
			globalSchema.err.errMsg = "alias is empty"
			return globalSchema.err.ToErr()
		}
		opt.alias = aliasList
	} else {
		globalSchema.err.errMsg = "alias is a required tag"
		return globalSchema.err.ToErr()
	}

	if fullDesc, exist := fieldTag.Tag.Lookup(DescriptionFullTagName); exist {
		opt.desc = fullDesc
	} else {
		if shortDesc, exist := fieldTag.Tag.Lookup(DescriptionShortTagName); exist {
			opt.desc = shortDesc
		}
	}

	if defaultValueStr, exist := fieldTag.Tag.Lookup(DefaultTagName); exist {
		opt.defaultStr = defaultValueStr
		// 转换默认值
		if defaultValue, transformErr := transform(defaultValueStr, opt.optionFieldType); transformErr != nil {
			globalSchema.err.errMsg = transformErr.Error()
			return globalSchema.err.ToErr()
		} else {
			opt.defaultVal = defaultValue
		}
	}

	if _, exist := fieldTag.Tag.Lookup(RequiredTagName); exist {
		opt.required = true
	}

	return nil
}

func (opt *option) reflectFillField(belongOptG *optionGroup, optGField *reflect.StructField) error {
	globalSchema.err.process = "Reflect Fill Field"
	if belongOptG == nil {
		globalSchema.err.errMsg = "belong option group is nil"
		return globalSchema.err.ToErr()
	}
	opt.belongPtr = belongOptG
	if optGField == nil {
		globalSchema.err.errMsg = "struct field is nil"
		return globalSchema.err.ToErr()
	}
	opt.optionFieldPtr = optGField
	opt.optionFieldType = optGField.Type
	opt.optionFieldName = optGField.Name
	return opt.reflectFillFieldTag()
}

type optionGroup struct {
	optionGroupPath string

	structName string
	structPtr  interface{}
	structType reflect.Type
	// alias => *option
	options map[string]*option
	// field name => *option
	fieldToOptions map[string]*option

	usageHelper usageProvider
	validator   validateProvider

	// relation weak blind
	parentType      reflect.Type
	parentName      string
	parentFieldName string
}

func (optGroup *optionGroup) toTable(style Style, indent int) *Table {
	if optGroup == nil || optGroup.structType == nil {
		return nil
	}

	// indent 表示整张表左侧缩进的“字符数”（目前我们假定主要是空格）
	tbl := InitRenderTable(indent)

	// 为了保持字段输出顺序稳定，按 struct 字段顺序来遍历，
	// 再用 fieldToOptions 做映射，而不是直接遍历 map。
	for i := 0; i < optGroup.structType.NumField(); i++ {
		field := optGroup.structType.Field(i)
		opt, ok := optGroup.fieldToOptions[field.Name]
		if !ok {
			continue
		}

		// 列 1：alias（带 - / -- 等样式）
		aliasStr := formatAlias(opt, style)

		// 列 2：Required / Dispensable
		reqStr := "Dispensable Option"
		if opt.required {
			reqStr = "Required Option"
		}

		// 列 3：描述 + 默认值
		desc := opt.desc
		descWithDefault := desc
		if opt.defaultStr != "" {
			if descWithDefault != "" {
				descWithDefault += "  "
			}
			descWithDefault += fmt.Sprintf("(default: %s)", opt.defaultStr)
		}

		// 创建每一行对应的 3 个 Cell
		row := []*Cell{
			NewTableCell(aliasStr),
			NewTableCell(reqStr),
			NewTableCell(descWithDefault),
		}
		tbl.AppendRowWithCells(row)
	}

	return tbl
}

func (optGroup *optionGroup) updateCurrentPath(parentPath string) {
	if parentPath == "" {
		optGroup.optionGroupPath = optGroup.structName
	} else {
		optGroup.optionGroupPath = parentPath + "." + optGroup.structName
	}
	for _, opt := range optGroup.fieldToOptions {
		opt.optionPath = optGroup.optionGroupPath + "." + opt.optionFieldName
		if opt.subStructPtr != nil {
			opt.subStructPtr.updateCurrentPath(optGroup.optionGroupPath)
		}
	}
}

func (optGroup *optionGroup) mountChildren(children []*optionGroup) error {
	for _, child := range children {
		opt, exist := optGroup.fieldToOptions[child.parentFieldName]
		if !exist {
			globalSchema.err.process = "Build Tree"
			globalSchema.err.errMsg = fmt.Sprintf(
				"parent type %s exists but has no field %s for child %s",
				child.parentType.Name(),
				child.parentFieldName,
				child.structName,
			)
			return globalSchema.err.ToErr()
		}
		if opt.subStructPtr != nil && opt.subStructPtr != child {
			globalSchema.err.process = "Build Tree"
			globalSchema.err.errMsg = fmt.Sprintf(
				"field %s.%s already mounted to %s, cannot remount %s",
				optGroup.structName,
				opt.optionFieldName,
				opt.subStructPtr.structName,
				child.structName,
			)
			return globalSchema.err.ToErr()
		}
		opt.subStructPtr = child
	}
	return nil
}

func (optGroup *optionGroup) registerOption(opt *option) error {
	globalSchema.err.process = "Register Option"
	if opt == nil {
		globalSchema.err.errMsg = "option is nil"
		return globalSchema.err.ToErr()
	}
	if optGroup.fieldToOptions == nil {
		optGroup.fieldToOptions = make(map[string]*option)
	}
	if _, exist := optGroup.fieldToOptions[opt.optionFieldName]; exist {
		globalSchema.err.errMsg = fmt.Sprintf("duplicate field %s in struct %s", opt.optionFieldName, optGroup.structName)
		return globalSchema.err.ToErr()
	}
	optGroup.fieldToOptions[opt.optionFieldName] = opt

	if optGroup.options == nil {
		optGroup.options = make(map[string]*option)
	}
	for _, optAlias := range opt.alias {
		if _, exist := optGroup.options[optAlias]; exist {
			globalSchema.err.errMsg = fmt.Sprintf("duplicate register, key[%s] already exists", optAlias)
			return globalSchema.err.ToErr()
		}
		optGroup.options[optAlias] = opt
	}
	return nil
}

func (optGroup *optionGroup) reflectFillStruct(newOptG interface{}) error {
	if newOptG == nil {
		globalSchema.err.process = "Reflect Fill Struct"
		globalSchema.err.errMsg = "newOptG is nil"
		return globalSchema.err.ToErr()
	}

	optGVal := reflect.ValueOf(newOptG)
	optGType := optGVal.Type()
	switch optGVal.Kind() {
	case reflect.Ptr:
		if optGVal.IsNil() {
			globalSchema.err.process = "Reflect Fill Struct"
			globalSchema.err.errMsg = "optG is a nil pointer"
			return globalSchema.err.ToErr()
		}
		if optGVal.Elem().Kind() != reflect.Struct {
			globalSchema.err.process = "Reflect Fill Struct"
			globalSchema.err.errMsg = "optG must point to a struct"
			return globalSchema.err.ToErr()
		}
		optGroup.structPtr = newOptG
		optGroup.structType = optGType.Elem()
		optGroup.structName = optGType.Elem().Name()
	case reflect.Struct:
		p := reflect.New(optGType)
		p.Elem().Set(optGVal)
		optGroup.structPtr = p.Interface()
		optGroup.structType = optGType
		optGroup.structName = optGType.Name()
	default:
		globalSchema.err.process = "Reflect Fill Struct"
		globalSchema.err.errMsg = fmt.Sprintf("the passed-in %T is neither a struct nor a *struct", newOptG)
		return globalSchema.err.ToErr()
	}

	if u, ok := optGroup.structPtr.(usageProvider); ok {
		optGroup.usageHelper = u
	}
	if v, ok := optGroup.structPtr.(validateProvider); ok {
		optGroup.validator = v
	}

	return nil
}

func (optGroup *optionGroup) reflectFillParentStruct(parentOptG interface{}) error {
	if parentOptG == nil {
		return nil
	}
	pt := reflect.TypeOf(parentOptG)
	for pt.Kind() == reflect.Ptr {
		pt = pt.Elem()
	}
	if pt.Kind() != reflect.Struct {
		globalSchema.err.process = "Reflect Fill Parent Struct"
		globalSchema.err.errMsg = fmt.Sprintf("parentOptG must be struct/*struct, got %v", pt)
		return globalSchema.err.ToErr()
	}
	optGroup.parentType = pt
	optGroup.parentName = pt.Name()
	return nil
}

func Register(parentG interface{}, parentOptName string, optG interface{}) {
	registerMutex.Lock()
	defer registerMutex.Unlock()
	globalSchema.err.funcName = "Register"
	newOptionGroup := &optionGroup{options: make(map[string]*option), fieldToOptions: map[string]*option{}}

	// 填充结构
	if err := newOptionGroup.reflectFillStruct(optG); err != nil {
		panic(err)
	}

	if parentG != nil {
		// 判断所设字段名是否在父结构中存在
		if exist, err := hasField(parentG, parentOptName); err != nil {
			globalSchema.err.process = "Judge Parent Option Name Exist"
			globalSchema.err.errMsg = err.Error()
			panic(globalSchema.err.ToErr())
		} else if !exist {
			globalSchema.err.process = "Judge Parent Option Name Exist"
			globalSchema.err.errMsg = fmt.Sprintf("%s isn't exist in %v", parentOptName, parentG)
			panic(globalSchema.err.ToErr())
		}
		newOptionGroup.parentFieldName = parentOptName

		// 填充父结构
		if err := newOptionGroup.reflectFillParentStruct(parentG); err != nil {
			panic(err)
		}
	}

	for i := 0; i < newOptionGroup.structType.NumField(); i++ {
		field := newOptionGroup.structType.Field(i)
		newOption := &option{}
		// 填充成员
		if err := newOption.reflectFillField(newOptionGroup, &field); err != nil {
			panic(err)
		} else {
			if err = newOptionGroup.registerOption(newOption); err != nil {
				panic(err)
			}
		}
	}

	if err := globalSchema.storeOptionGroup(newOptionGroup); err != nil {
		panic(err)
	}
}
