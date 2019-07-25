package gofaas

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/schollz/faas/pkg/utils"
	log "github.com/schollz/logger"
)

type Param struct {
	Name string
	Type string
}

var ErrorFunctionNotFound = errors.New("function not found")

// ParseFunctionString takes input like ParseFunctionString("x","y","z"),`run(1,"hello",[1.2,2.1])`)
// and returns []byte(`{"x":1,"y":"hello","z":[1.2,2.1]}`) which can later be used for unmarshalling
func ParseFunctionString(paramNames []string, functionString string) (functionName string, jsonBytes []byte, err error) {
	if !strings.Contains(functionString, "(") {
		err = fmt.Errorf("must contain ()")
		log.Error(err)
		return
	}
	// add brackets
	foo := strings.SplitN(functionString, "(", 2)
	functionName = strings.TrimSpace(foo[0])
	functionString = strings.TrimSpace(foo[1])
	functionString = "[" + functionString[:len(functionString)-1] + "]"

	var values []interface{}
	err = json.Unmarshal([]byte(functionString), &values)
	if err != nil {
		log.Error(err)
		return
	}

	if len(values) != len(paramNames) {
		err = fmt.Errorf("number of values and param names not equal")
		log.Error(err)
		return
	}

	// build JSON string
	jsonString := ""
	for i, value := range values {
		var valueByte []byte
		valueByte, err = json.Marshal(value)
		if err != nil {
			log.Error(err)
			return
		}
		jsonString += `"` + paramNames[i] + `": ` + string(valueByte)
		if i < len(values)-1 {
			jsonString += ", "
		}
	}

	jsonString = "{" + jsonString + "}"
	jsonBytes = []byte(jsonString)
	return
}

func FindFunction(importPath string, functionName string) (structString string, err error) {
	// create a temp directory
	tempdir, err := ioutil.TempDir("", "parser")
	if err != nil {
		log.Error(err)
		return
	}
	log.Debugf("cloning %s into %s", importPath, tempdir)
	defer os.RemoveAll(tempdir)

	// clone into temp directory
	stdout, stderr, err := utils.RunCommand(fmt.Sprintf("git clone --depth 1 https://%s %s", importPath, tempdir))
	log.Debugf("stdout: [%s]", stdout)
	log.Debugf("stderr: [%s]", stderr)
	if err != nil {
		log.Error(err)
		return
	}
	if strings.Contains(stderr, "fatal") {
		err = fmt.Errorf("%s", stderr)
		return
	}

	// find all go files
	goFiles := []string{}
	err = filepath.Walk(tempdir,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(path, ".go") {
				goFiles = append(goFiles, path)
			}
			return nil
		})
	if err != nil {
		log.Error(err)
		return
	}
	log.Debugf("found %d go files", len(goFiles))

	// return the function
	return
}

func FindFunctionInFile(fname string, functionName string) (packageName string, inputParams []Param, outputParams []Param, err error) {
	// read file
	b, err := ioutil.ReadFile(fname)
	if err != nil {
		log.Error(err)
		return
	}
	src := string(b)

	// create token set
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, fname, src, 0)
	if err != nil {
		log.Error(err)
		return
	}
	offset := f.Pos()

	// look for function in file, default is not found
	err = ErrorFunctionNotFound
	ast.Inspect(f, func(n ast.Node) bool {
		if fd, ok := n.(*ast.File); ok {
			packageName = fd.Name.Name
		}
		if fd, ok := n.(*ast.FuncDecl); ok {
			if fd.Name.Name != functionName {
				return true
			}
			// found function
			err = nil
			inputParams = make([]Param, len(fd.Type.Params.List))
			for i, param := range fd.Type.Params.List {
				inputParams[i] = Param{
					param.Names[0].Name,
					src[param.Type.Pos()-offset : param.Type.End()-offset],
				}
			}
			outputParams = make([]Param, len(fd.Type.Results.List))
			for i, param := range fd.Type.Results.List {
				outputParams[i] = Param{
					param.Names[0].Name,
					src[param.Type.Pos()-offset : param.Type.End()-offset],
				}
			}
		}
		return true
	})
	if packageName == "" {
		err = errors.New("no package name")
	}
	return
}

func codeGeneration(packageName string, functionName string, inputParams []Param, outputParams []Param) (code string, err error) {

	funcMap := template.FuncMap{
		// The name "title" is what the function will be called in the template text.
		"title": strings.Title,
	}

	const templateText = `
type Input struct {
	{{- range .InputParams }}
	{{title .Name }} {{.Type }} ` + "`" + `json:"{{.Name}}"` + "`" + `{{ end }}
}

var params Input
err = json.Unmarshal(b, &params)
{{range $index, $element := .OutputParams }}{{if $index}}, {{end}}{{$element.Name}}{{ end }} := {{.FunctionName}}(
	{{- range .InputParams}}
	params.{{title .Name }},{{end }}
)

// create json
fullJson = "{"
{{range $index, $element := .OutputParams }}
{{if $index}}fullJson += ","{{end}}
b, err = json.Marshal({{.Name}})
if err != nil {
	log.Error(err)
	return
}
fullJson +=  ` + "`" + `"{{.Name}}": ` + "`" + ` + string(b)
{{end}}
fullJson += "}"
`

	type TemplateStruct struct {
		FunctionName string
		InputParams  []Param
		OutputParams []Param
	}

	tmpl, err := template.New("titleTest").Funcs(funcMap).Parse(templateText)
	if err != nil {
		log.Error(err)
		return
	}

	var tpl bytes.Buffer
	err = tmpl.Execute(&tpl, TemplateStruct{
		packageName + "." + functionName, inputParams, outputParams,
	})
	if err != nil {
		log.Error(err)
		return
	}

	codeBytes, err := format.Source(tpl.Bytes())
	code = string(codeBytes)
	return
}