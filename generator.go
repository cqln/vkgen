package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io/ioutil"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/cqln/vkgen/schema"
	"github.com/yudai/pp"
)

const (
	genPrefix = "// Code generated by vkgen; DO NOT EDIT."
	pkgName   = "generated"
)

type Generator struct {
	parser        *schema.Parser
	nofmt         bool
	nogoify       bool
	debug         bool
	goifyReplacer *strings.Replacer
}

func NewGenerator(nofmt, nogoify, debug bool, objectsSchema []byte) Generator {
	repl := []string{
		"_", "",
		" ", "",
		".", "",
		"2fa", "TwoFA",
		"json", "JSON",
		"Id", "ID",
		"Ttl", "TTL",
		"Sdk", "SDK",
		"Vk", "VK",
		"Tv", "TV",
		"Url", "URL",
	}

	return Generator{
		parser:        schema.NewParser(objectsSchema),
		nofmt:         nofmt,
		nogoify:       nogoify,
		debug:         debug,
		goifyReplacer: strings.NewReplacer(repl...),
	}
}

func (g Generator) Generate() (err error) {
	err = g.generateObjects()
	if err != nil {
		return err
	}

	err = g.generateResponses()
	if err != nil {
		return fmt.Errorf("responses: %w", err)
	}

	err = g.generateMethods()
	if err != nil {
		return fmt.Errorf("methods: %w", err)
	}

	err = g.generateMethodsTypeSafe()
	if err != nil {
		return fmt.Errorf("methods type-safe: %w", err)
	}

	err = g.generateBuilders()
	if err != nil {
		return fmt.Errorf("builders: %w", err)
	}

	err = g.generateRequests()
	if err != nil {
		return fmt.Errorf("requests: %w", err)
	}

	return
}

var kekRules = map[string]map[string]map[string]string{
	"generated/objects.gen.go": {
		"NotificationsNotificationParent": {
			"Likes": "*BaseLikesInfo",
		},
	},
	// "generated/responses.gen.go": {
	// 	"NewsfeedGetSuggestedSourcesResponse": {
	// 		"Items.IsClosed": "omgkek",
	// 	},
	// },
}

func (g Generator) writeSource(name string, b *bytes.Buffer) error {
	p, err := NewPatcher(b.Bytes())
	if err != nil {
		return fmt.Errorf("patcher: %w", err)
	}

	rulesForThisFile, ok := kekRules[name]
	if ok {
		for structName, rules := range rulesForThisFile {
			for fieldName, chTo := range rules {
				err := p.PatchStruct(structName, ChangeField(fieldName, chTo))
				if err != nil {
					return fmt.Errorf("patcher: %w", err)
				}
			}
		}
	}

	src, err := p.Src()
	if err != nil {
		return fmt.Errorf("patcher: %w", err)
	}
	if g.nofmt {
		return ioutil.WriteFile(name, src, 0677)
	}

	src, err = format.Source(src)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(name, src, 0677)
}

type callback = func(b *bytes.Buffer, schema []byte) error

func (g Generator) generate(schemaFile, outputName string, cb callback) error {
	sch, err := ioutil.ReadFile(schemaFile)
	if err != nil {
		return err
	}

	b := bytes.NewBuffer(nil)
	b.WriteString(genPrefix + "\n\npackage " + pkgName + "\n")

	err = cb(b, sch)
	if err != nil {
		return err
	}

	return g.writeSource(outputName, b)
}

func (g Generator) generateObjects() error {
	return g.generate("objects.json", pkgName+"/objects.gen.go",
		func(b *bytes.Buffer, objectsSchema []byte) error {
			objects, err := g.parser.ParseObjects(objectsSchema)
			if err != nil {
				return err
			}
			b.WriteString("\nimport \"encoding/json\"\n\n")
			for _, object := range objects {
				b.WriteString(g.ObjectDefinitionToGolang(object) + "\n")
			}

			return nil
		})
}

func (g Generator) generateResponses() error {
	return g.generate("responses.json", pkgName+"/responses.gen.go",
		func(b *bytes.Buffer, responsesSchema []byte) error {
			responses, err := g.parser.ParseResponses(responsesSchema)
			if err != nil {
				return err
			}

			for _, response := range responses {
				typ := g.ResponseDefinitionToGolang(response)
				b.WriteString(typ + "\n")
			}
			return nil
		})
}

func (g Generator) generateMethods() error {
	return g.generate("methods.json", pkgName+"/methods.gen.go",
		func(b *bytes.Buffer, methodsSchema []byte) error {
			methods, err := g.parser.ParseMethods(methodsSchema)
			if err != nil {
				return err
			}

			for _, method := range methods {
				for _, response := range method.Responses {
					extended := strings.Contains(strings.ToLower(response.Name), "extended")
					if method.Description != nil {
						b.WriteString("// " + *method.Description + "\n")
					}
					methodPostfix := g.goify(response.Name)
					if len(method.Responses) == 1 || response.Name == "response" {
						methodPostfix = ""
					}
					if strings.HasSuffix(response.Name, "Response") {
						repl := strings.ReplaceAll(response.Name, "Response", "")
						if repl != "" {
							methodPostfix = g.goify(repl)
						}
					}

					gresponse := g.objectExprToGolang(response.Expr)
					if gresponse == "StorageGetWithKeysResponse" {
						methodPostfix = "With" + methodPostfix
					}
					b.WriteString("func (vk *VK) " + g.goify(method.Name) + methodPostfix + "(params Params) (response " + gresponse + ", err error) {\n")
					if extended {
						b.WriteString("\tparams[\"extended\"] = true\n")
					}
					b.WriteString("\terr = vk.RequestUnmarshal(\"" + method.Name + "\", params, &response)\n")
					b.WriteString("\treturn\n")
					b.WriteString("}")
					b.WriteString("\n\n")
				}
			}
			return nil
		})
}

func (g Generator) generateMethodsTypeSafe() error {
	return g.generate("methods.json", pkgName+"/methods_safe.gen.go",
		func(b *bytes.Buffer, methodsSchema []byte) error {
			methods, err := g.parser.ParseMethods(methodsSchema)
			if err != nil {
				return err
			}

			for _, method := range methods {
				for _, response := range method.Responses {
					extended := strings.Contains(strings.ToLower(response.Name), "extended")
					if method.Description != nil {
						b.WriteString("// " + *method.Description + "\n")
					}
					methodPostfix := g.goify(response.Name)
					if len(method.Responses) == 1 || response.Name == "response" {
						methodPostfix = ""
					}
					if strings.HasSuffix(response.Name, "Response") {
						repl := strings.ReplaceAll(response.Name, "Response", "")
						if repl != "" {
							methodPostfix = g.goify(repl)
						}
					}
					gresponse := g.objectExprToGolang(response.Expr)
					if gresponse == "StorageGetWithKeysResponse" {
						methodPostfix = "With" + methodPostfix
					}
					b.WriteString("func (vk *VK) " + g.goify(method.Name) + methodPostfix + "Safe(req " + g.goify(method.Name) + ") (response " + gresponse + ", err error) {\n")
					if extended {
						b.WriteString("\tparams := req.params()\n")
						b.WriteString("\tparams[\"extended\"] = true\n")
						b.WriteString("\terr = vk.RequestUnmarshal(\"" + method.Name + "\", params, &response)\n")
					} else {
						b.WriteString("\terr = vk.RequestUnmarshal(\"" + method.Name + "\", req.params(), &response)\n")
					}

					b.WriteString("\treturn\n")
					b.WriteString("}")
					b.WriteString("\n\n")
				}
			}
			return nil
		})
}

func (g Generator) generateBuilders() error {
	return g.generate("methods.json", pkgName+"/builders.gen.go",
		func(b *bytes.Buffer, methodsSchema []byte) error {
			b.WriteString("import \"github.com/SevereCloud/vksdk/api\"\n\n")
			methods, err := g.parser.ParseMethods(methodsSchema)
			if err != nil {
				return err
			}

			for _, method := range methods {
				// define struct
				builderName := g.goify(method.Name) + `Builder`
				b.WriteString("// " + builderName + " builder.\n")
				b.WriteString("// \n")
				if method.Description != nil {
					b.WriteString("// " + *method.Description + "\n")
					b.WriteString("// \n")
				}

				b.WriteString("// https://vk.com/dev/" + method.Name + "\n")
				b.WriteString(`type ` + builderName + ` struct {` + "\n")
				b.WriteString("\tapi.Params\n")
				b.WriteString("}\n\n")

				// define constructor
				b.WriteString("// " + builderName + " func.\n")
				b.WriteString("func New" + builderName + "() *" + builderName + " {\n")
				b.WriteString("\treturn &" + builderName + "{api.Params{}}\n")
				b.WriteString("}\n\n")

				for _, parameter := range method.Parameters {
					if parameter.Description != nil {
						b.WriteString("// " + *parameter.Description + "\n")
					}

					gparam := g.objectExprToGolang(parameter.ObjectExpr)
					aLevel := strings.Count(gparam, "[]")
					gparam = strings.ReplaceAll(gparam, "[]", "")
					_, isBuiltin := builtinTypes[gparam]
					if !isBuiltin {
						gparam = "api." + gparam
					}
					if aLevel == 1 {
						gparam = "..." + gparam
					} else {
						for i := 0; i < aLevel; i++ {
							gparam = "[]" + gparam
						}
					}
					b.WriteString("func (b *" + builderName + ") " + g.goify(parameter.Name) + "(v " + gparam + ") *" + builderName + " {\n")
					b.WriteString("\tb.Params[\"" + parameter.Name + "\"] = v\n")
					b.WriteString("\treturn b\n")
					b.WriteString("}\n\n")
				}
			}
			return nil
		})
}

func (g Generator) generateRequests() error {
	return g.generate("methods.json", pkgName+"/requests.gen.go",
		func(b *bytes.Buffer, methodsSchema []byte) error {
			methods, err := g.parser.ParseMethods(methodsSchema)
			if err != nil {
				return err
			}

			for _, method := range methods {
				// define struct
				requestName := g.goify(method.Name)
				b.WriteString("// " + requestName + ".\n")
				b.WriteString("// \n")
				if method.Description != nil {
					b.WriteString("// " + *method.Description + "\n")
					b.WriteString("// \n")
				}

				b.WriteString("// https://vk.com/dev/" + method.Name + "\n")
				b.WriteString("type " + requestName + " struct{\n")
				for _, parameter := range method.Parameters {
					paramName := g.goify(parameter.Name)
					paramType := g.objectExprToGolang(parameter.ObjectExpr)
					if _, isBuiltin := builtinTypes[paramType]; !isBuiltin && !strings.HasPrefix(paramType, "[]") {
						paramType = "*" + paramType
					}
					b.WriteString("\t" + paramName + " " + paramType)
					if parameter.Description != nil {
						b.WriteString("// " + *parameter.Description)
					}
					b.WriteString("\n")
				}
				b.WriteString("}\n\n")

				b.WriteString("func (req " + requestName + ") params() Params {\n")
				b.WriteString("\tparams := make(Params)\n")
				for _, parameter := range method.Parameters {
					pname := g.goify(parameter.Name)
					ptype := g.objectExprToGolang(parameter.ObjectExpr)
					b.WriteString("\tif ")
					if strings.HasPrefix(ptype, "[]") {
						b.WriteString("len(req." + pname + ") > 0")
					} else if ptype == "bool" {
						b.WriteString("req." + pname)
					} else if ptype == "string" {
						b.WriteString("req." + pname + " != \"\"")
					} else if ptype == "int64" || ptype == "float64" {
						b.WriteString("req." + pname + " != 0")
					} else {
						b.WriteString("req." + pname + " != nil")
					}

					b.WriteString(" {\n")
					b.WriteString("\t\tparams[\"" + parameter.Name + "\"] = req." + g.goify(parameter.Name) + "\n")
					b.WriteString("\t}\n")
				}
				b.WriteString("\treturn params\n")
				b.WriteString("}\n\n")

			}
			return nil
		})
}

func (g Generator) goify(name string) string {
	if g.nogoify {
		return name
	}

	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	for i, r := range runes {
		if r == '_' || r == ' ' || r == '.' {
			if i+1 == len(runes) {
				break
			}
			runes[i+1] = unicode.ToUpper(runes[i+1])
		}
	}

	return g.goifyReplacer.Replace(string(runes))
}

func (g Generator) ObjectDefinitionToGolang(obj schema.ObjectDefinition) string {
	var sb strings.Builder
	if obj.Expr.Description != nil {
		sb.WriteString("// " + *obj.Expr.Description + "\n")
	}

	gname := g.goify(obj.Name)
	if gname == "LeadsComplete" || gname == "LeadsStart" {
		gname += "Object"
	}
	if obj.Expr.Is(schema.Base | schema.Ref | schema.Array) {
		gtype := g.objectExprToGolang(obj.Expr)
		// alias
		if isBuiltin(gtype) {
			sb.WriteString("type " + gname + " = " + gtype + "\n")
			return sb.String()
		}
		sb.WriteString("type " + gname + " " + gtype + "\n")
		return sb.String()
	}

	if obj.Expr.Is(schema.Enum) {
		sb.WriteString("type " + gname + " " + g.objectExprToGolang(obj.Expr) + "\n")
		if len(obj.Expr.Enum) == 0 {
			return sb.String()
		}

		sb.WriteString("\nconst (\n")
		for idx, item := range obj.Expr.Enum {
			val := "undefined"
			isString := false
			switch obj.Expr.Type {
			case "number":
				val = strconv.FormatFloat(item.(float64), 'g', 10, 64)
			case "integer":
				val = strconv.FormatInt(item.(int64), 10)
			case "string":
				val = item.(string)
				isString = true
			default:
				panic("unsupported enum type")
			}

			fieldNamePostfix := val
			if len(obj.Expr.EnumNames) > 0 {
				fieldNamePostfix = obj.Expr.EnumNames[idx]
			}

			if isString {
				val = `"` + val + `"`
			}

			fieldName := gname + g.goify(fieldNamePostfix)
			sb.WriteString("\t" + fieldName + " " + gname + " = " + val + "\n")
		}
		sb.WriteString(")\n")
		return sb.String()
	}

	if obj.Expr.Is(schema.AllOf) {
		s := "// allof " + obj.Name
		s = "type " + g.goify(obj.Name) + " " + g.allofOneofExprToGolang(obj.Expr) + "\n"
		return s
	}

	if obj.Expr.Is(schema.OneOf) {
		s := "// oneof" + obj.Name
		s = "type " + g.goify(obj.Name) + " " + g.allofOneofExprToGolang(obj.Expr)
		return s
	}

	sb.WriteString("type " + gname + " struct {\n")
	for _, prop := range obj.Expr.Properties {
		jsonTag := "`json:\"" + prop.Name
		jsonTag += "\"`"
		goType := g.objectExprToGolang(prop.Expr)

		if prop.Expr.Is(schema.Ref) {
			ref, err := prop.Expr.Ref()
			if err != nil {
				panic(err)
			}
			if obj.Name == *&ref.Name {
				goType = "*" + goType
			}
		}

		if prop.Expr.Description != nil {
			jsonTag += " // " + *prop.Expr.Description
		}

		sb.WriteString("\t" + g.goify(prop.Name) + " " + goType + " " + jsonTag + "\n")
	}

	sb.WriteString("}\n")
	return sb.String()
}

func (g Generator) objectExprToGolang(expr schema.ObjectExpr) string {
	if expr.Is(schema.Ref) {
		ref, err := expr.Ref()
		if err != nil {
			panic(err)
		}
		return g.goify(*&ref.Name)
	}

	if expr.Is(schema.AllOf | schema.OneOf) {
		return g.allofOneofExprToGolang(expr)
	}

	switch expr.Type {
	case "integer":
		return "int64"
	case "number":
		return "float64"
	case "string":
		return "string"
	case "boolean":
		return "bool"
	case "array":
		return "[]" + g.objectExprToGolang(*expr.ArrayOf)
	case "object":
		if len(expr.Properties) > 0 {
			var sb strings.Builder
			sb.WriteString("struct{\n")
			for _, prop := range expr.Properties {
				jtag := "`json:\"" + prop.Name + "\"`"
				sb.WriteString("\t" + g.goify(prop.Name) + " " + g.objectExprToGolang(prop.Expr) + " " + jtag + "\n")
			}
			sb.WriteString("}\n")
			return sb.String()
		}
		fallthrough
	default:
		return "interface{}"
	}
}

var responseRules = map[string]string{
	"messages_delete_response": "map[string]int64",
}

func (g Generator) ResponseDefinitionToGolang(resp schema.ResponseDefinition) string {
	var sb strings.Builder
	if resp.Expr.Description != nil {
		sb.WriteString("// " + *resp.Expr.Description + "\n")
	}
	gname := g.goify(resp.Name)
	if !strings.HasSuffix(gname, "Response") {
		gname = gname + "Response"
	}
	if forcedType, ok := responseRules[resp.Name]; ok {
		sb.WriteString("type " + gname + " " + forcedType + "\n")
		return sb.String()
	}

	if resp.Expr.Is(schema.Base | schema.Ref | schema.Array) {
		gtype := g.objectExprToGolang(resp.Expr.ObjectExpr)
		// alias
		if isBuiltin(gtype) {
			sb.WriteString("type " + gname + " = " + gtype + "\n")
			return sb.String()
		}
		sb.WriteString("type " + gname + " " + gtype + "\n")
		return sb.String()
	}

	if resp.Expr.Is(schema.Enum) {
		if resp.Expr.Description != nil {
			sb.WriteString("// " + *resp.Expr.Description + "\n")
		}
		sb.WriteString("type " + gname + " " + g.objectExprToGolang(resp.Expr.ObjectExpr) + "\n")
		if len(resp.Expr.Enum) == 0 {
			return sb.String()
		}

		sb.WriteString("\nconst (\n")
		for idx, item := range resp.Expr.Enum {
			val := "undefined"
			isString := false
			switch resp.Expr.ObjectExpr.Type {
			case "number":
				val = strconv.FormatFloat(item.(float64), 'g', 10, 64)
			case "integer":
				val = strconv.FormatInt(item.(int64), 10)
			case "string":
				val = item.(string)
				isString = true
			default:
				panic("unsupported enum type")
			}

			fieldNamePostfix := val
			if len(resp.Expr.EnumNames) > 0 {
				fieldNamePostfix = resp.Expr.EnumNames[idx]
			}

			if isString {
				val = `"` + val + `"`
			}

			fieldName := gname + g.goify(fieldNamePostfix)
			sb.WriteString("\t" + fieldName + " " + gname + " = " + val + "\n")
		}
		sb.WriteString(")\n")
		return sb.String()
	}

	if resp.Expr.Is(schema.AllOf) {
		s := "// allof" + resp.Name
		s = "type " + g.goify(resp.Name) + " " + g.allofOneofExprToGolang(resp.Expr.ObjectExpr)
		return s
	}

	if resp.Expr.Is(schema.OneOf) {
		s := "// oneof" + resp.Name
		s = "type " + g.goify(resp.Name) + " " + g.allofOneofExprToGolang(resp.Expr.ObjectExpr)
		return s
	}

	requiredFields := make(map[string]struct{})
	for _, field := range resp.Expr.Required {
		requiredFields[field] = struct{}{}
	}
	allFieldsRequired := len(requiredFields) == 0
	sb.WriteString("type " + gname + " struct {\n")
	for _, prop := range resp.Expr.Properties {
		jsonTag := "`json:\"" + prop.Name
		ptr := false
		if _, required := requiredFields[prop.Name]; !required && !allFieldsRequired {
			jsonTag += ",omitempty"
			ptr = true
		}
		jsonTag += "\"`"
		goType := g.objectExprToGolang(prop.Expr)

		if prop.Expr.Is(schema.Ref) {
			ref, err := prop.Expr.Ref()
			if err != nil {
				panic(err)
			}
			if resp.Name == *&ref.Name || ptr {
				goType = "*" + goType
			}
		}

		if prop.Expr.Description != nil {
			jsonTag += " // " + *prop.Expr.Description
		}

		sb.WriteString("\t" + g.goify(prop.Name) + " " + goType + " " + jsonTag + "\n")
	}

	sb.WriteString("}\n")
	return sb.String()
}

func (g Generator) allofOneofExtractFields(expr schema.ObjectExpr) map[string][]schema.ObjectExpr {
	if !expr.Is(schema.AllOf | schema.OneOf) {
		panic("unsupported obj type")
	}

	var iterValues []schema.ObjectExpr
	if expr.Is(schema.AllOf) {
		iterValues = expr.AllOf
	} else {
		iterValues = expr.OneOf
	}

	fields := make(map[string][]schema.ObjectExpr)
	for _, val := range iterValues {
		if val.Is(schema.Ref) {
			ref, err := val.Ref()
			if err != nil {
				panic(err)
			}
			if ref.Expr.Is(schema.AllOf | schema.OneOf) {
				for name, allofFields := range g.allofOneofExtractFields(ref.Expr) {
					tmp, ok := fields[name]
					if !ok {
						tmp = make([]schema.ObjectExpr, 0)
					}
					tmp = append(tmp, allofFields...)
					fields[name] = tmp
				}
				continue
			}

			if ref.Expr.Is(schema.Ref) {
				panic("reference expr. unimplemented")
			}

			for _, prop := range ref.Expr.Properties {
				tmp, ok := fields[prop.Name]
				if !ok {
					tmp = make([]schema.ObjectExpr, 0)
				}
				tmp = append(tmp, prop.Expr)
				fields[prop.Name] = tmp
			}
			continue
		}

		if len(val.Properties) == 0 {
			panic("allof no props")
		}
		for _, prop := range val.Properties {
			tmp, ok := fields[prop.Name]
			if !ok {
				tmp = make([]schema.ObjectExpr, 0)
			}
			tmp = append(tmp, prop.Expr)
			fields[prop.Name] = tmp
		}
	}
	return fields
}

func (g Generator) allofOneofExprToGolang(expr schema.ObjectExpr) string {
	var sb strings.Builder
	mergingFields := g.allofOneofExtractFields(expr)
	var keys []string
	for name := range mergingFields {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	sb.WriteString("struct{\n")
	for _, name := range getAllofOneofFieldNames(expr) {
		sb.WriteString("\t// " + name + "\n")
	}
	for _, propName := range keys {
		fields := mergingFields[propName]
		if len(fields) == 0 {
			panic("no fields")
		}
		if len(fields) == 1 {
			f := fields[0]
			sb.WriteString("\t" + g.goify(propName) + " *" + g.objectExprToGolang(f) + "`json:\"" + propName + ",omitempty\"`")
			if f.Description != nil {
				sb.WriteString("// " + *f.Description)
			}
			sb.WriteString("\n")
			continue
		}

		equal := true
		for i := 1; i < len(fields); i++ {
			if !fields[i-1].EqualType(fields[i]) {
				equal = false
				break
			}
		}

		if equal {
			f := fields[0]
			sb.WriteString("\t" + g.goify(propName) + " *" + g.objectExprToGolang(f) + "`json:\"" + propName + ",omitempty\"`")
			if f.Description != nil {
				sb.WriteString("// " + *f.Description)
			}
			sb.WriteString("\n")
			continue
		}
		sb.WriteString("\t" + g.goify(propName) + " json.RawMessage `json:\"" + propName + ",omitempty\"`\n")
	}

	if sb.Len() == 0 {
		panic("allof empty code")
	}
	sb.WriteString("}")
	return sb.String()
}

var builtinTypes = map[string]struct{}{
	"int64":   {},
	"float64": {},
	"string":  {},
	"bool":    {},
}

func isBuiltin(s string) bool {
	s = strings.ReplaceAll(s, "[]", "")
	_, ok := builtinTypes[s]
	return ok
}

func getAllofOneofFieldNames(expr schema.ObjectExpr) []string {
	var names []string
	var fields []schema.ObjectExpr
	if expr.Is(schema.AllOf) {
		fields = expr.AllOf
	} else if expr.Is(schema.OneOf) {
		fields = expr.OneOf
	} else {
		panic("unsupported obj type")
	}

	for _, field := range fields {
		if field.Is(schema.Ref) {
			ref, err := field.Ref()
			if err != nil {
				panic(err)
			}
			names = append(names, ref.Name)
			continue
		}
		if field.Type == "object" {
			str := "struct{\n"
			for _, prop := range field.Properties {
				str += "\t" + prop.Name + "\n"
			}
			str += "}"
			names = append(names, str)
			continue
		}
		pp.Println(field)
	}
	return names
}
