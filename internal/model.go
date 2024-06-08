package internal

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
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
	Name          string
	Type          TypeName
	Substitutions []SubstitutionMetadata
}

type SubstitutionMetadata struct {
	index      int
	formatSpec string
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
	return &GoStruct{b.queryConst.Name + "Params", fields}
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
	formatSpec := fieldBuilder.ExplicitFormatSpecifier
	if formatSpec == "" {
		spec, err := determineFormatSpecifier(b.pass.Pkg.Scope(), fieldBuilder.Name, fieldBuilder.TypeName)
		if err != nil {
			return nil, err
		}
		formatSpec = spec
	}
	return &GoStructField{
		fieldBuilder.Name,
		TypeName{fieldBuilder.TypeName},
		[]SubstitutionMetadata{
			{
				index:      fieldBuilder.Index,
				formatSpec: formatSpec,
			},
		},
	}, nil
}

func (b *goStructBuilder) mergeFieldData(field *GoStructField, fieldBuilder GoStructFieldBuilder) error {
	if fieldBuilder.TypeName != "_" && fieldBuilder.TypeName != field.Type.Name {
		return errors.Newf("field %v used with distinct types: %v and %v",
			field.Name, field.Type.Name, fieldBuilder.TypeName)
	}
	formatSpec := fieldBuilder.ExplicitFormatSpecifier
	if formatSpec == "" {
		spec, err := determineFormatSpecifier(b.pass.Pkg.Scope(), field.Name, field.Type.Name)
		if err != nil {
			return err
		}
		formatSpec = spec
	}
	field.Substitutions = append(field.Substitutions, SubstitutionMetadata{
		index:      fieldBuilder.Index,
		formatSpec: formatSpec,
	})
	return nil
}

func determineFormatSpecifier(scope *types.Scope, varName string, typeName string) (string, error) {
	_, obj := scope.LookupParent(typeName, token.NoPos)
	if obj == nil {
		return "", errors.Newf("failed name lookup for type %v of interpolation variable %v", typeName, varName)
	}
	if _, ok := obj.(*types.TypeName); !ok {
		return "", errors.Newf("expected named type after first ':' but found %v", obj.String())
	}
	if formatSpecFromType, ok := determineFormatSpecifierFromType(obj.Type()); ok {
		return formatSpecFromType, nil
	}
	return "", &cannotAutomaticallyFormatError{typeName}
}

func determineFormatSpecifierFromType(typ types.Type) (string, bool) {
	// TODO: Is this only going to handle the 1-level deep case?
	basicType, ok := typ.Underlying().(*types.Basic)
	if !ok {
		return "", false
	}
	// See TestSqlf; it looks like the format specifiers are basically ignored :O
	switch basicType.Kind() {
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
		return "%d", true
	case types.String:
		return "%s", true
	default:
	}
	return "", false
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

		buf.WriteString(fmt.Sprintf("var _ %sQueryParams = &%s{}\n\n", packagePrefix, goStruct.TypeName))

		type perIndexData = struct {
			fieldName  string
			formatSpec string
		}
		indexToData := map[int]perIndexData{}
		for _, field := range goStruct.Fields {
			for _, subst := range field.Substitutions {
				indexToData[subst.index] = perIndexData{fieldName: field.Name, formatSpec: subst.formatSpec}
			}
		}

		buf.WriteString(fmt.Sprintf("func (qp *%s) FormatSpecifiers() []string {\n", goStruct.TypeName))
		buf.WriteString("\treturn []string{")
		// This might seem a bit silly. The Postgres binding syntax allows $1, $2 etc.
		// So why duplicate the formatters & arguments? Because some databases like
		// SQLite and MySQL don't seem to support similar syntax.
		// https://github.com/keegancsmith/sqlf/blob/master/bindvar.go#L19-L20
		for i := 0; i < len(indexToData); i++ {
			buf.WriteString(fmt.Sprintf("\"%s\",", indexToData[i].formatSpec))
		}
		buf.WriteString("}\n")
		buf.WriteString("}\n\n")

		buf.WriteString(fmt.Sprintf("func (qp *%s) FormatArgs() []any {\n", goStruct.TypeName))
		buf.WriteString("\treturn []any{")
		for i := 0; i < len(indexToData); i++ {
			buf.WriteString(fmt.Sprintf("qp.%s,", indexToData[i].fieldName))
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
