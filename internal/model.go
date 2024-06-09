package internal

import (
	"bytes"
	"fmt"
	"go/ast"
	"golang.org/x/tools/go/analysis"

	"github.com/grafana/regexp"
	"github.com/wk8/go-ordered-map/v2"

	"github.com/sourcegraph/sourcegraph/lib/errors"
)

type StructFactory struct {
	Pass *analysis.Pass
}

// NewGoStruct attempts to build a GoStruct from the given query string.
//
// There are 3 outcomes:
// 1. If the query was using string interpolation and is well-formed: returns a GoStruct
// 2. If the query was using string interpolation and is ill-formed: returns nil, err
// 3. If the query was not using string interpolation: returns nil, nil
func (factory *StructFactory) NewGoStruct(subregex *regexp.Regexp, queryConst *ast.Ident, templateText string) (*GoStruct, error) {
	structBuilder := newStructBuilder(factory.Pass, queryConst)
	for i, matches := range subregex.FindAllStringSubmatch(templateText, -1) {
		if err := structBuilder.AddInterpolationMatch(i, matches); err != nil {
			factory.Pass.Reportf(queryConst.Pos(), "ill-formed interpolation: %v", err)
			return nil, err
		}
	}
	return structBuilder.tryBuild(), nil
}

type GoStruct struct {
	TypeName string
	Fields   []GoStructField
}

type GoStructField struct {
	Name    string
	Type    TypeName
	Indexes []int
}

type TypeName struct {
	Name string
}

type goStructBuilder struct {
	pass       *analysis.Pass
	queryConst *ast.Ident
	fieldMap   *orderedmap.OrderedMap[string, *GoStructField]
}

func newStructBuilder(pass *analysis.Pass, queryConst *ast.Ident) goStructBuilder {
	return goStructBuilder{
		pass,
		queryConst,
		orderedmap.New[string, *GoStructField](),
	}
}

func (b *goStructBuilder) tryBuild() *GoStruct {
	if b.fieldMap.Len() == 0 {
		return nil
	}
	var fields []GoStructField
	for it := b.fieldMap.Oldest(); it != nil; it = it.Next() {
		fields = append(fields, *it.Value)
	}
	return &GoStruct{b.queryConst.Name + "Vars", fields}
}

func (b *goStructBuilder) AddInterpolationMatch(index int, matches []string) error {
	fieldBuilder := NewFieldBuilder(index, matches)

	if fieldData, found := b.fieldMap.Get(fieldBuilder.Name); found {
		return b.emitExtraTypeHint(fieldBuilder, b.mergeFieldData(fieldData, fieldBuilder))
	}

	newFieldData, err := b.createNewField(fieldBuilder)
	if err != nil {
		return b.emitExtraTypeHint(fieldBuilder, err)
	}

	b.fieldMap.Set(newFieldData.Name, newFieldData)
	return nil
}

func (b *goStructBuilder) createNewField(fieldBuilder GoStructFieldBuilder) (*GoStructField, error) {
	if fieldBuilder.TypeName == "_" {
		return nil, errors.Newf("first interpolation of %v must specify type", fieldBuilder.Name)
	}
	return &GoStructField{
		fieldBuilder.Name,
		TypeName{fieldBuilder.TypeName},
		[]int{fieldBuilder.Index},
	}, nil
}

func (b *goStructBuilder) mergeFieldData(field *GoStructField, fieldBuilder GoStructFieldBuilder) error {
	if fieldBuilder.TypeName != "_" && fieldBuilder.TypeName != field.Type.Name {
		return errors.Newf("field %v used with distinct types: %v and %v",
			field.Name, field.Type.Name, fieldBuilder.TypeName)
	}
	field.Indexes = append(field.Indexes, fieldBuilder.Index)
	return nil
}

func WriteStructs(wanted []GoStruct, buf *bytes.Buffer, shouldImportInterpolate bool) {
	packagePrefix := "interpolate."
	if !shouldImportInterpolate {
		packagePrefix = ""
	}
	for j, goStruct := range wanted {
		buf.WriteString(fmt.Sprintf("type %s struct {\n", goStruct.TypeName))
		for _, field := range goStruct.Fields {
			buf.WriteString(fmt.Sprintf("\t%s %s\n", field.Name, field.Type.Name))
		}
		buf.WriteString("}\n\n")

		buf.WriteString(fmt.Sprintf("var _ %sQueryVars = &%s{}\n\n", packagePrefix, goStruct.TypeName))

		fieldNameForIndex := map[int]string{}
		for _, field := range goStruct.Fields {
			for _, index := range field.Indexes {
				fieldNameForIndex[index] = field.Name
			}
		}

		buf.WriteString(fmt.Sprintf("func (qp *%s) FormatArgs() []any {\n", goStruct.TypeName))
		buf.WriteString("\treturn []any{")
		for i := 0; i < len(fieldNameForIndex); i++ {
			buf.WriteString(fmt.Sprintf("qp.%s,", fieldNameForIndex[i]))
		}
		buf.WriteString("}\n")
		if j == len(wanted)-1 {
			buf.WriteString("}\n")
		} else {
			buf.WriteString("}\n\n")
		}

	}
}

type cannotAutomaticallyFormatError struct {
	typeName string
}

var _ error = &cannotAutomaticallyFormatError{}

func (c *cannotAutomaticallyFormatError) Error() string {
	return "cannot automatically format type: " + c.typeName
}

func (b *goStructBuilder) emitExtraTypeHint(fieldBuilder GoStructFieldBuilder, err error) error {
	if errors.HasType[*cannotAutomaticallyFormatError](err) {
		b.pass.Reportf(b.queryConst.Pos(),
			"cannot handle type %v of interpolation variable %v;"+
				" it should be a basic type (int, uint, string) "+
				"or have a basic type as its underlying type",
			fieldBuilder.TypeName, fieldBuilder.Name)
		b.pass.Reportf(b.queryConst.Pos(),
			"HINT: you can specify a custom format specifier using {{fieldName : type : %%d}} syntax")
		return err
	}
	return err
}
