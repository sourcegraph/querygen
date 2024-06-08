package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"io"
	"os"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/sourcegraph/conc/pool"
	"github.com/sourcegraph/log"
	"github.com/sourcegraph/sourcegraph/lib/errors"

	"github.com/sourcegraph/querygen/internal"
)

func main() {
	liblog := log.Init(log.Resource{Name: "querygen", InstanceID: "none"})
	defer liblog.Sync()

	// Checking mode vs modifying mode
	multichecker.Main(newAnalyzer())
}

func newAnalyzer() *analysis.Analyzer {
	return &analysis.Analyzer{
		Name:             "querygen",
		Doc:              "Gather queries from Go source code",
		Run:              run,
		RunDespiteErrors: true,
	}
}

type analysisResult struct {
	file    *token.File         // non-nil
	astFile *ast.File           // non-nil
	wanted  []internal.GoStruct // may be empty
}

type queryGenFileData struct {
	file    *token.File // may be nil
	astFile *ast.File   // may be nil
	// 2-phased initialization, may be empty
	wanted []internal.GoStruct
}

func run(pass *analysis.Pass) (any, error) {
	logger := log.Scoped("querygen")

	queryGenFiles, results := gatherQueryGenFileData(logger, pass)
	logger.Debug("gathered query file data",
		log.Int("numQueryGenFiles", len(queryGenFiles)),
		log.Int("numNonQueryGenFiles", len(results)))

	for _, result := range results {
		logger.Debug("got result", log.String("path", result.file.Name()), log.Int("wanted", len(result.wanted)))
		if len(result.wanted) == 0 {
			continue
		}
		if !strings.HasSuffix(result.file.Name(), ".go") {
			// Erm, got a query in non-Go code?
			continue
		}

		queryGenFilename := getQueryGenFilename(result.file.Name())
		if fileData, ok := queryGenFiles[queryGenFilename]; ok {
			fileData.wanted = result.wanted
		} else {
			fileData = &queryGenFileData{nil, nil, result.wanted}
			queryGenFiles[queryGenFilename] = fileData
		}
	}
	logger.Debug("updated query file data", log.Int("numQueryGenFiles", len(queryGenFiles)))

	updateQueryGenFiles(logger.Scoped("io"), pass.Pkg, pass.Fset, queryGenFiles)

	return nil, nil
}

func updateQueryGenFiles(logger log.Logger, pkg *types.Package, fset *token.FileSet, queryGenFiles map[string]*queryGenFileData) {
	shouldImportInterpolate :=
		pkg.Path() != "github.com/sourcegraph/querygen/lib/interpolate" &&
			pkg.Path() != "github.com/sourcegraph/querygen/lib/interpolate.test"

	// All the possibilities:
	//
	//	| a.go    | Num queries | a_query_gen.go | Action                     |
	//	|---------|-------------|----------------|----------------------------|
	//	| Present | 1+          | Up-to-date     | Update file (no-op)        |
	//	| Present | 1+          | Stale          | Update file (creates diff) |
	//	| Present | 1+          | Absent         | Create file                |
	//	| Present | 0           | Present        | Remove file                |
	//	| Absent  | n/a         | Present        | Remove file                |
	for path, fileData := range queryGenFiles {
		logger := logger.With(log.String("path", path))
		// Handle the creation case
		if fileData.file == nil {
			logger.Debug("creating new file")
			if err := fileData.createNewFile(pkg, path, shouldImportInterpolate); err != nil {
				logger.Error("failed to write generated structs", log.Error(err))
			}
			continue
		}
		// Handle both removal cases
		if len(fileData.wanted) == 0 {
			logger.Debug("removing file")
			if err := os.Remove(path); err != nil {
				logger.Warn("failed to remove path")

			}
		}
		// Handle the update case
		logger.Debug("updating file")
		if err := fileData.updateFile(fset, shouldImportInterpolate); err != nil {
			logger.Warn("failed to update file", log.Error(err))
		}
	}
}

func gatherQueryGenFileData(logger log.Logger, pass *analysis.Pass) (map[string]*queryGenFileData, []analysisResult) {
	p := pool.NewWithResults[analysisResult]()
	queryGenFiles := map[string]*queryGenFileData{}
	nonGenFilePaths := internal.Set[string]{}
	for _, astFile := range pass.Files {
		file := pass.Fset.File(astFile.FileStart)
		if file == nil { // malformed code/bug
			continue
		}
		logger := logger.With(log.String("path", file.Name()))
		if isQueryGenFilePath(file.Name()) {
			logger.Debug("not visiting file")
			queryGenFiles[file.Name()] = &queryGenFileData{file, astFile, nil}
			continue
		}
		nonGenFilePaths.Add(file.Name())
		p.Go(func() analysisResult {
			logger.Debug("visiting file")
			visitor := internal.NewQueryGenVisitor(logger, pass)
			ast.Walk(visitor, astFile)
			return analysisResult{file, astFile, visitor.ParamStructs}
		})
	}
	results := p.Wait()
	return queryGenFiles, results
}

func getQueryGenFilename(original string) string {
	if strings.HasSuffix(original, "_query.go") {
		return original[:len(original)-len("_query.go")] + "_query_gen.go"
	}
	if strings.HasSuffix(original, "_queries.go") {
		return original[:len(original)-len("_queries.go")] + "_query_gen.go"
	}
	if strings.HasSuffix(original, "_test.go") && !strings.HasSuffix(original, "_query_gen_test.go") {
		return original[:len(original)-len("_test.go")] + "_query_gen_test.go"
	}
	return original[:len(original)-len(".go")] + "_query_gen.go"
}

func isQueryGenFilePath(p string) bool {
	return strings.HasSuffix(p, "_query_gen.go") || strings.HasSuffix(p, "_query_gen_test.go")
}

func (fileData *queryGenFileData) createNewFile(pkg *types.Package, path string, shouldImportInterpolate bool) error {
	var buf bytes.Buffer
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return errors.Wrap(err, "failed to create file")
	}
	buf.WriteString("// Code generated by querygen.\n")
	buf.WriteString("// You may only edit import statements.\n")
	buf.WriteString(fmt.Sprintf("package %s\n", pkg.Name()))
	buf.WriteRune('\n')

	if shouldImportInterpolate {
		buf.WriteString("import (\n")
		buf.WriteString("\t\"github.com/sourcegraph/querygen/lib/interpolate\"\n")
		buf.WriteString(")\n")
	}

	internal.WriteStructs(fileData.wanted, &buf, shouldImportInterpolate)
	formattedBytes, _ := format.Source(buf.Bytes())
	if _, err := file.Write(formattedBytes); err != nil {
		return errors.Wrap(err, "failed to write to newly created file")
	}
	return nil
}

func (fileData *queryGenFileData) updateFile(fset *token.FileSet, shouldImportInterpolate bool) error {
	// 1-based line number
	pkgKeywordLine := fset.Position(fileData.astFile.Package).Line
	lastImportStatementEndLine := -1
	for _, decl := range fileData.astFile.Decls {
		if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.IMPORT {
			lastImportStatementEndLine = max(lastImportStatementEndLine, fset.Position(genDecl.End()).Line)
		}
	}
	prefixLineCount := max(pkgKeywordLine, lastImportStatementEndLine)

	fileHandle, err := os.OpenFile(fileData.file.Name(), os.O_RDWR, 0644)
	if err != nil {
		return errors.Wrap(err, "failed to open query gen file")
	}
	fileContents, err := io.ReadAll(fileHandle)
	if err != nil {
		return errors.Wrap(err, "failed to read query gen file")
	}

	lines := 0
	prefixByteCount := len(fileContents)
	for i, c := range fileContents {
		if c == '\n' {
			lines += 1
			if lines > prefixLineCount {
				prefixByteCount = i + 1
				break
			}
		}
	}

	if err := fileHandle.Truncate(int64(prefixByteCount)); err != nil {
		return errors.Wrap(err, "failed to truncate file")
	}

	if _, err := fileHandle.Seek(0, io.SeekEnd); err != nil {
		return errors.Wrap(err, "failed to seek to end of file")
	}

	var buf bytes.Buffer
	internal.WriteStructs(fileData.wanted, &buf, shouldImportInterpolate)
	formattedBytes, _ := format.Source(buf.Bytes())
	if _, err := fileHandle.Write(formattedBytes); err != nil {
		return errors.Wrap(err, "failed to write data")
	}
	return nil
}
