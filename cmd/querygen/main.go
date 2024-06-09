package main

import (
	"bytes"
	"crypto/sha1"
	"flag"
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

	"github.com/charmbracelet/log"
	"github.com/sourcegraph/conc/pool"
	"github.com/sourcegraph/sourcegraph/lib/errors"

	"github.com/sourcegraph/querygen/internal"
)

func main() {
	defaultLevel := globalLogLevel
	flag.StringVar(&globalLogLevel, "log-level", defaultLevel, "Log level: one of debug, info, warn, error, or fatal")
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

var globalLogLevel string = "info"

func initLogger() (*log.Logger, error) {
	level, err := log.ParseLevel(globalLogLevel)
	if err != nil {
		return nil, err
	}
	styles := log.DefaultStyles()
	for lvl, value := range styles.Levels {
		styles.Levels[lvl] = value.MaxWidth(8)
	}
	logger := log.NewWithOptions(os.Stderr, log.Options{ReportTimestamp: true, Level: level})
	logger.SetTimeFormat("15:04:05")
	logger.SetStyles(styles)
	return logger, nil
}

func run(pass *analysis.Pass) (any, error) {
	logger, err := initLogger()
	if err != nil {
		return nil, err
	}

	queryGenFiles, results := gatherQueryGenFileData(logger, pass)
	logger.Debug("gathered query file data",
		"numQueryGenFiles", len(queryGenFiles),
		"numNonQueryGenFiles", len(results))

	for _, result := range results {
		logger.Debug("got result", "path", result.file.Name(), "wanted", len(result.wanted))
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
	logger.Debug("updated query file data", "numQueryGenFiles", len(queryGenFiles))

	updateQueryGenFiles(logger, pass.Pkg, pass.Fset, queryGenFiles)

	return nil, nil
}

func updateQueryGenFiles(logger *log.Logger, pkg *types.Package, fset *token.FileSet, queryGenFiles map[string]*queryGenFileData) {
	shouldImportInterpolate :=
		pkg.Path() != "github.com/sourcegraph/querygen/lib/interpolate" &&
			pkg.Path() != "github.com/sourcegraph/querygen/lib/interpolate.test"

	createdFileCount := 0
	updatedFileCount := 0

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
		logger := logger.With("path", path)
		// Handle the creation case
		if fileData.file == nil {
			logger.Debug("creating new file")
			if err := fileData.createNewFile(pkg, path, shouldImportInterpolate); err != nil {
				logger.Error("failed to write generated structs", "err", err)
			} else {
				createdFileCount += 1
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
		modified, err := fileData.updateFile(fset, shouldImportInterpolate)
		if err != nil {
			logger.Warn("failed to update file", "err", err)
		} else if modified {
			updatedFileCount += 1
		}
	}

	if createdFileCount != 0 || updatedFileCount != 0 {
		logger.Info("querygen codegen summary",
			"pkg", pkg.Path(),
			"filesCreated", createdFileCount,
			"filesUpdated", updatedFileCount)
	}
}

func gatherQueryGenFileData(logger *log.Logger, pass *analysis.Pass) (map[string]*queryGenFileData, []analysisResult) {
	p := pool.NewWithResults[analysisResult]()
	queryGenFiles := map[string]*queryGenFileData{}
	nonGenFilePaths := internal.Set[string]{}
	for _, astFile := range pass.Files {
		file := pass.Fset.File(astFile.FileStart)
		if file == nil { // malformed code/bug
			continue
		}
		logger := logger.With("path", file.Name())
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

func (fileData *queryGenFileData) updateFile(fset *token.FileSet, shouldImportInterpolate bool) (modified bool, _ error) {
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
		return false, errors.Wrap(err, "failed to open query gen file")
	}
	fileContents, err := io.ReadAll(fileHandle)
	if err != nil {
		return false, errors.Wrap(err, "failed to read query gen file")
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

	restOfFile := fileContents[prefixByteCount:]

	var buf bytes.Buffer
	internal.WriteStructs(fileData.wanted, &buf, shouldImportInterpolate)
	formattedBytes, _ := format.Source(buf.Bytes())

	if sha1.Sum(restOfFile) == sha1.Sum(formattedBytes) {
		return false, nil
	}

	if err := fileHandle.Truncate(int64(prefixByteCount)); err != nil {
		return false, errors.Wrap(err, "failed to truncate file")
	}

	if _, err := fileHandle.Seek(0, io.SeekEnd); err != nil {
		return true, errors.Wrap(err, "failed to seek to end of file")
	}

	if _, err := fileHandle.Write(formattedBytes); err != nil {
		return true, errors.Wrap(err, "failed to write data")
	}
	return true, nil
}
