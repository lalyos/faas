package gofaas

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
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

type CodeGen struct {
	ImportPath   string
	PackageName  string
	FunctionName string
	InputParams  []Param
	OutputParams []Param
}

var ErrorFunctionNotFound = errors.New("function not found")

func BuildContainer(importPathOrURL string, functionName string, containerName string) (err error) {
	// create a temp directory
	tempdir, err := ioutil.TempDir("", "build")
	if err != nil {
		log.Error(err)
		return
	}
	if log.GetLevel() != "debug" {
		defer os.RemoveAll(tempdir)
	}
	log.Debugf("working in %s", tempdir)

	if strings.HasPrefix(importPathOrURL, "http") {
		err = GenerateContainerFromURL(importPathOrURL, functionName, tempdir)
		if err != nil {
			log.Error(err)
			return
		}
	} else {
		err = GenerateContainerFromImportPath(importPathOrURL, functionName, tempdir)
		if err != nil {
			log.Error(err)
			return
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		log.Error(err)
		return
	}

	imagesPath := path.Join(cwd, "images")

	defer os.Chdir(cwd)
	os.Chdir(tempdir)

	stdout, stderr, err := utils.RunCommand(fmt.Sprintf("docker build -t %s .", containerName))
	log.Debugf("stdout: [%s]", stdout)
	log.Debugf("stderr: [%s]", stderr)
	if stderr != "" {
		err = fmt.Errorf("%s\n%s", stdout, stderr)
		return
	}

	stdout, stderr, err = utils.RunCommand(fmt.Sprintf("docker save %s -o %s", containerName, path.Join(imagesPath, containerName+".tar")))
	log.Debugf("stdout: [%s]", stdout)
	log.Debugf("stderr: [%s]", stderr)
	if stderr != "" {
		err = fmt.Errorf("%s\n%s", stdout, stderr)
		return
	}

	stdout, stderr, err = utils.RunCommand(fmt.Sprintf("gzip %s", path.Join(imagesPath, containerName+".tar")))
	log.Debugf("stdout: [%s]", stdout)
	log.Debugf("stderr: [%s]", stderr)
	if stderr != "" {
		err = fmt.Errorf("%s\n%s", stdout, stderr)
		return
	}

	return
}

func GenerateContainerFromURL(urlString string, functionName string, tempdir string) (err error) {
	log.Debugf("building %s into %s", urlString, tempdir)
	resp, err := http.Get(urlString)
	if err != nil {
		log.Error(err)
		return
	}
	defer resp.Body.Close()

	out, err := os.Create(path.Join(tempdir, "1.go"))
	if err != nil {
		log.Error(err)
		return
	}
	defer out.Close()
	io.Copy(out, resp.Body)

	// build the template file
	funcMap := template.FuncMap{
		"title": strings.Title,
	}
	tmpl, err := template.New("titleTest").Funcs(funcMap).Parse(string(_MainGo))
	if err != nil {
		log.Error(err)
		return
	}

	packageName, inputParams, outputParams, err := FindFunctionInFile(path.Join(tempdir, "1.go"), functionName)
	if err != nil {
		log.Error(err)
		return
	}
	log.Debugf("packageName: %+v", packageName)
	log.Debugf("inputParams: %+v", inputParams)
	log.Debugf("outputParams: %+v", outputParams)

	var tpl bytes.Buffer
	err = tmpl.Execute(&tpl, CodeGen{
		ImportPath:   "",
		PackageName:  "",
		FunctionName: functionName,
		InputParams:  inputParams,
		OutputParams: outputParams,
	})
	if err != nil {
		log.Error(err)
		return
	}

	b, err := ioutil.ReadFile(path.Join(tempdir, "1.go"))
	if err != nil {
		log.Error(err)
		return
	}
	b = bytes.Replace(b, []byte("package "+packageName), []byte("package main"), 1)
	err = ioutil.WriteFile(path.Join(tempdir, "1.go"), b, 0644)
	if err != nil {
		log.Error(err)
		return
	}

	code := tpl.String()
	err = ioutil.WriteFile(path.Join(tempdir, "main.go"), []byte(code), 0644)
	if err != nil {
		log.Error(err)
		return
	}

	err = ioutil.WriteFile(path.Join(tempdir, "Dockerfile"), []byte(Dockerfile), 0644)
	if err != nil {
		log.Error(err)
		return
	}

	err = ioutil.WriteFile(path.Join(tempdir, "go.mod"), []byte(`module main`), 0644)
	if err != nil {
		log.Error(err)
		return
	}
	return
}

func GenerateContainerFromImportPath(importPath string, functionName string, tempdir string) (err error) {
	log.Debugf("building %s into %s", importPath, tempdir)

	// build the template file
	b, _ := ioutil.ReadFile("pkg/gofaas/template/main.go")
	funcMap := template.FuncMap{
		"title": strings.Title,
	}
	tmpl, err := template.New("titleTest").Funcs(funcMap).Parse(string(b))
	if err != nil {
		log.Error(err)
		return
	}

	packageName, inputParams, outputParams, err := FindFunctionInImportPath(importPath, functionName)
	if err != nil {
		log.Error(err)
		return
	}
	log.Debugf("packageName: %+v", packageName)
	log.Debugf("inputParams: %+v", inputParams)
	log.Debugf("outputParams: %+v", outputParams)

	var tpl bytes.Buffer
	err = tmpl.Execute(&tpl, CodeGen{
		ImportPath:   importPath,
		PackageName:  packageName,
		FunctionName: functionName,
		InputParams:  inputParams,
		OutputParams: outputParams,
	})
	if err != nil {
		log.Error(err)
		return
	}

	code := tpl.String()
	log.Debug("=== main.go ===", code)
	err = ioutil.WriteFile(path.Join(tempdir, "main.go"), []byte(code), 0644)
	if err != nil {
		log.Error(err)
		return
	}

	err = ioutil.WriteFile(path.Join(tempdir, "Dockerfile"), []byte(Dockerfile), 0644)
	if err != nil {
		log.Error(err)
		return
	}

	err = ioutil.WriteFile(path.Join(tempdir, "go.mod"), []byte(`module main`), 0644)
	if err != nil {
		log.Error(err)
		return
	}

	return
}

// FindFunctionInImportPath takes an import path and a function name and returns
// all the nessecary components to generate the file
func FindFunctionInImportPath(importPath string, functionName string) (packageName string, inputParams []Param, outputParams []Param, err error) {
	// create a temp directory
	tempdir, err := ioutil.TempDir("", "parser")
	if err != nil {
		log.Error(err)
		return
	}
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

	// loop through the files to find the function
	for _, fname := range goFiles {
		packageName, inputParams, outputParams, err = FindFunctionInFile(fname, functionName)
		if err == nil {
			for i := range inputParams {
				inputParams[i].Type = UpdateTypeWithPackage(packageName, inputParams[i].Type)
			}
			for i := range inputParams {
				outputParams[i].Type = UpdateTypeWithPackage(packageName, outputParams[i].Type)
			}
			return
		}
	}
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
			log.Debugf("found funnction: %s", fd.Name.Name)
			if fd.Name.Name != functionName {
				return true
			}
			// found function
			err = nil
			inputParams = make([]Param, len(fd.Type.Params.List))
			for i, param := range fd.Type.Params.List {
				if len(param.Names) == 0 {
					continue
				}
				inputParams[i] = Param{
					param.Names[0].Name,
					src[param.Type.Pos()-offset : param.Type.End()-offset],
				}
			}
			outputParams = make([]Param, len(fd.Type.Results.List))
			for i, param := range fd.Type.Results.List {
				if len(param.Names) == 0 {
					continue
				}
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

var types = []string{"error", "string", "bool", "byte", "int8", "uint8", "int16", "uint16", "int32", "uint32", "int64", "uint64", "int", "uint", "uintptr", "float32", "float64", "complex64", "complex128"}

func UpdateTypeWithPackage(packageName string, typeString string) (newTypeString string) {
	typeString = strings.TrimPrefix(typeString, "...")
	newTypeString = typeString
	if strings.Contains(typeString, ".") {
		// don't handle the other types yet
		return
	}
	isarray := typeString[:2] == "[]"
	if isarray {
		typeString = typeString[2:]
	}
	ispointer := string(typeString[0]) == string("*")
	if ispointer {
		typeString = typeString[1:]
	}
	isnormal := false
	for _, t := range types {
		if typeString == t {
			isnormal = true
			break
		}
	}
	if isnormal {
		return newTypeString
	}
	newTypeString = ""
	if isarray {
		newTypeString += "[]"
	}
	if ispointer {
		newTypeString += "*"
	}
	newTypeString += packageName + "." + typeString
	return
}

const Dockerfile = `
##################################
# 1. Build in a Go-based image   #
###################################
FROM golang:1.14-alpine as builder
RUN apk add git
WORKDIR /go/main
COPY . .
ENV GO111MODULE=on
RUN go build -v

###################################
# 2. Copy into a clean image     #
###################################
FROM alpine:latest
RUN apk update && apk add ca-certificates && rm -rf /var/cache/apk/*
COPY --from=builder /go/main/main /main
EXPOSE 8080
ENTRYPOINT ["/main"]
# any flags here, for example use the data folder
CMD ["--debug"] 
`

var _MainGo = []byte("\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\xff\xac\x58\x7b\x6f\xdc\xb8\x11\xff\x5b\xfc\x14\x13\xe2\x72\xd0\x26\x5a\xed\xa6\x2d\x8a\xc0\xc0\xf6\xe0\xe4\x7c\xb9\x00\x71\xe2\x7a\x9d\xb6\x40\x10\xd4\xb4\x34\xd2\x32\x96\xc8\x3d\x92\xf2\xda\x11\xf4\xdd\x8b\x21\xa5\x7d\xf9\x99\xb6\x7f\xac\x25\x91\xf3\xfc\x71\x66\x38\xe3\xa5\xc8\x2e\x45\x89\x50\x0b\xa9\x18\x93\xf5\x52\x1b\x07\x31\x8b\x38\xaa\x4c\xe7\x52\x95\x93\x6f\x56\x2b\xce\x22\x5e\x54\xa2\xf4\xcf\xda\xd1\x43\xa1\x9b\x2c\x9c\x5b\xd2\xbb\x75\x26\xd3\xea\x8a\x5e\x9d\xac\x91\x9e\xda\x72\xc6\x22\x5e\x4a\xb7\x68\x2e\xd2\x4c\xd7\x13\x9b\x2d\x74\x55\x7d\x9f\x14\x42\xd8\xc9\xf2\xb2\x9c\x94\x9a\x5e\x39\x8b\x2a\x5d\xc2\x5d\x94\x95\x2e\x4b\x34\x24\x67\x32\x01\xeb\x84\x71\x50\xa2\x42\x23\x1c\xe6\x90\xe9\x1c\x59\xd4\xb6\x2b\xe9\x16\x90\xbe\xf7\x86\x9f\x08\xb7\xe8\x3a\xde\xb6\xa9\xff\x8b\x2a\xef\x3a\xcf\x8c\x2a\xdf\x67\x1d\x31\x76\x9f\xd4\x4c\x2b\xeb\xa0\x68\x54\xe6\xa4\x56\x1f\x45\x8d\x67\xfa\xb4\x51\x30\x03\x12\xfd\xdb\xd6\x7a\xd7\x71\x76\x25\x0c\x2c\x85\x11\x35\x2d\x58\x98\xc1\x97\xaf\xd6\x19\xa9\xca\x16\xda\xd6\x08\x55\x22\xfc\x24\x55\x8e\xd7\x09\xfc\x84\x15\xd6\xa8\x1c\x1c\xcc\x20\x7d\xaf\x96\x8d\x3b\x21\x46\x0b\x5d\xd7\xb6\xb2\xe8\xe9\xba\x2e\x81\xde\x78\xde\xb6\x03\x4f\xda\xeb\x6b\x5b\xef\x4d\xd7\x41\xc7\xdc\xcd\x12\xc1\xcb\x01\xeb\x4c\x93\x39\x68\x09\x92\x31\x04\xb5\x7b\x2a\x68\xcb\x49\x57\x21\x78\x59\x24\xa2\x6d\xd3\x33\x92\xd1\x75\x00\xe7\x74\xd0\x07\xe4\x61\xaf\xe9\x7c\x40\xb0\xeb\x11\x69\x2c\x9a\xb7\xda\x90\x8f\xce\x34\xc8\xee\x06\x96\xf0\xf0\xb0\x9e\xc9\x1a\x81\xe2\x21\xa5\x37\xc6\x18\x21\xea\x03\x2d\x1e\x91\xa1\x44\x98\xe3\x45\x53\xc2\x85\xd6\x15\x8b\x28\xc0\xd2\x37\x5a\x57\xff\x10\x26\xfe\xd9\xef\x24\xc0\xfd\x93\x27\x50\x88\xca\xe2\xf0\x0d\xb5\xce\x91\x8f\x7a\x9e\x13\x61\x2c\xc6\x23\x16\xc9\xa2\x17\xd8\xb2\x88\xa2\x2a\x9d\xa3\xfb\x80\x57\x58\xc5\xbd\x98\x11\x8b\x3a\xc0\xca\xe2\x1d\x14\x52\x15\xda\x13\x30\x16\x45\x1b\x07\x66\xc1\x85\x8f\x7a\x45\x1a\x4a\xed\xe3\x22\x38\x10\x15\xda\xf8\x67\xe4\x49\xe6\x15\xe2\x32\x7e\x35\x85\x17\x81\x65\x8e\x99\x56\xf9\x88\xf6\x49\xd3\xaf\x64\x41\x11\xf3\x6c\x81\xd9\xa5\x54\xa5\x27\x3a\x80\xe7\x7f\x4a\x5f\x15\xf0\x37\xf8\xf3\x74\x3a\xfd\x85\x27\x3d\xab\x54\x19\xc6\x6b\x23\x46\xbd\x2c\x1b\x8f\xbc\x38\x59\x3c\x46\xd6\x0b\x0c\xd6\x45\xda\xa6\x47\xd7\xd2\xc5\x53\xcf\xdd\x31\xff\xeb\xc8\x1f\x32\xec\xbd\x2a\x74\x11\x73\xd3\x28\x45\x66\x69\x05\xbe\x08\x3c\xb7\x3c\x01\xfe\x7a\xfa\x7a\x4a\xa8\x50\xaa\xa7\xbf\x0b\x95\x57\x48\x09\x10\xf3\x09\x4f\x60\xe1\xbf\xcd\xb0\xfd\x41\x5a\x87\xea\x50\xe5\x73\x34\x57\x18\xf3\x03\xcf\x9c\x80\x92\xd5\x88\x75\xfd\xf9\xf7\x3c\xf1\x0a\x3c\xcf\x29\xda\xa5\x56\x16\xff\x69\xa4\x43\x93\x80\x81\x17\xfd\xfa\x1f\x0d\x5a\xe7\x71\x26\x5f\xe7\x3e\x51\x0f\x76\x4f\x23\xc7\x02\xcd\xf6\x81\x6c\xb9\xf3\xdc\xfe\xf2\xdc\x06\x27\x4c\xfa\xf9\xf4\x43\x4a\xc5\x61\x78\x3f\x15\xab\xbf\x37\x68\x6e\x76\xe0\x5e\xab\x21\x90\x09\x9d\x10\xa2\xa6\xb7\x10\xbe\x7c\xbd\xb8\x71\x18\x16\xd1\xf8\x9f\x36\xe1\x53\xfa\x1c\xf4\xe9\xc6\xa2\x8b\xcf\x56\x94\x98\xc0\xbf\xc9\x5c\x4a\xaa\xf4\x58\x18\xbb\x10\x55\xec\xc9\x46\x2c\x1a\x44\x12\xcd\x1e\xc9\x26\x8f\x23\x2f\x06\x00\x42\x35\x19\xf2\xb3\xa1\x55\x7e\xce\xa2\xe8\x18\xad\xa7\xd8\xdd\xaf\xc3\xaa\xa7\x98\x37\x59\x86\xd6\xfa\xfc\x82\x75\x86\xdb\xb0\x4a\x14\x5d\xeb\x63\x9d\xf8\xe3\x60\xf6\x28\x61\x51\xd4\x2b\xa1\x57\x4a\xf4\x84\x45\x5d\xc8\x2e\x93\x1e\xa3\x5b\xe8\x1c\x66\x33\xe0\xef\x8e\xce\xb8\x37\x74\xe3\x0e\xc1\x32\xeb\x8f\xf8\x1d\xba\x78\x95\x80\xd9\xe4\xdc\xbe\x80\x93\x4f\xf3\x07\x25\x9c\x68\xbb\x11\xe1\xf5\xd3\xee\xb3\x19\x05\xd4\x1e\xdb\x43\x38\x3e\x0a\xe4\x13\x90\x7c\x1c\xca\x80\xe5\x1d\x60\x46\x68\x4c\x7a\x44\xb1\x12\x87\xef\x50\xc9\x88\x63\xed\xd7\xba\xb2\x92\x8c\x55\xfa\x3b\x8a\x1c\x4d\x4c\xf9\xec\x62\x7e\xe8\x75\x8c\xdf\x6a\xe5\x8c\xae\xc6\x87\x55\xa5\x57\xe3\x4f\x46\x96\x52\x51\x86\xbe\xa0\xf4\x7c\x8c\xe9\x58\x5c\x8f\x0f\xe9\x48\x81\xbf\xfe\xeb\x5f\xa6\xd3\xa7\xf0\x04\x45\xe1\xbc\x7c\x2d\x78\x77\x74\x96\xf8\x33\x7b\x32\x73\x20\xf1\xcc\xb4\x81\xca\x8d\xe9\xbe\x49\x60\xf8\xfa\x80\xaa\xa4\xb4\x24\xee\xa5\x1b\x1f\xf5\x4d\x47\x02\xff\x1a\xbf\x9d\x9f\xfe\x36\x3e\xd3\x97\xa8\x12\x38\x6c\xdc\x42\x1b\xf9\x5d\xd0\xc5\x4b\x9b\xc7\xe2\xfa\xe9\x56\xbc\x35\x98\xa3\x72\x52\x54\xde\x12\x0a\xe9\x50\xe8\xa3\xb6\xa5\xa0\xc4\x3f\xe0\x15\xc4\x15\x2a\x48\x3f\x35\x6e\x7d\x65\x8e\xfc\x9d\xb9\xaf\x61\xdb\x0f\x2f\x0d\xaf\xdd\x64\x59\x09\xa9\x48\x66\xdb\x52\xa4\x3f\x99\xef\x9b\xb8\x12\x36\x33\x72\xe9\x7a\xe6\xd0\xb1\xdc\xc7\x1b\xd0\xe2\x09\xf4\x0d\x57\xfa\xde\x69\x41\x86\xc7\x43\x26\x8c\xa8\x76\xad\x52\x5f\x4d\x37\x8b\x7b\xc5\x37\xe4\xd5\x13\xeb\x6f\xbc\x57\xff\x92\x4d\xed\xf3\x35\x37\x47\xba\xf5\xcd\xba\xd6\x7d\xc4\xd5\xaf\x61\x29\x36\xe9\x1b\x9d\xdf\x8c\xee\x28\x91\x21\xcb\x7b\xd6\x34\xd0\xc7\x3f\x0f\xe5\xf1\x76\xa2\x53\x5d\x0f\x29\x84\x86\xaa\x41\x64\xd0\x35\x46\xf9\x33\x0c\xaf\x50\xa2\x1b\x7c\x19\xea\xec\xae\xd7\xbe\x1e\xfd\x9f\x9c\x5e\xdf\xe7\xf1\xee\x7d\x42\x1d\x49\xa3\xb2\xb9\x2f\x02\x09\xe8\x4b\x82\x25\x90\xf8\xfd\x78\xf4\x85\x13\x01\xff\xea\xbd\x7c\xa6\x2f\xbd\x7b\x01\x8e\xa2\x76\xc1\xc9\x22\xe6\x2a\xf4\x19\x7d\x45\xf2\xa1\xfe\x00\x06\xdb\x06\x6d\xf4\xf7\xc6\x0c\xbd\x6a\xe2\xcf\xe7\xcd\x8d\x43\x1b\xdc\x39\x98\x41\x68\xc1\x43\x03\x35\xf4\xb5\x81\x39\xde\xb4\xb4\x09\x6c\x64\x7e\x99\x7e\xfd\xd1\x03\xf2\xe4\xb7\x9b\xe9\x67\xb3\x9d\xc5\x7b\x70\xa8\xa5\xad\x85\xcb\x16\x98\xaf\xa9\xed\xe3\x68\xdc\x13\x6f\x3e\x3e\x3f\xab\xba\xbf\x22\xb6\xe0\xf8\xef\x42\xef\x29\xb1\x77\x6b\x2b\x98\xf4\x78\x88\x3d\x30\xf2\x3c\x30\x54\x6c\xd7\xaf\x7b\xa7\x0a\xdd\xb8\xb6\x1d\x56\x37\xf3\xc4\xc1\x0c\x86\x71\xea\x24\x4c\x85\x61\x10\xf0\xe3\x54\xda\xf3\xde\x1a\x80\xe2\xff\x79\xc8\xf1\xb0\xa4\xc3\x6c\xb2\x37\xef\xac\xcd\x0b\xf3\x9a\x2c\x40\xab\xea\x06\xb4\x42\xd0\xde\x57\x9a\xbd\xea\x04\xdc\x02\x15\x7c\x6b\xac\x83\xfe\x78\x41\x3a\x10\x2a\x87\xa5\x91\xca\x81\x74\xec\x09\xb5\xfe\x47\xa0\x65\xc3\xf9\xcd\xfa\x03\x8c\x29\x6e\xe7\x5e\x1d\xb5\x9f\x2f\xaf\x78\x12\xef\x20\xed\x6b\xf3\x50\xe3\xc3\x45\xe1\xb5\x52\xb4\x5e\xac\xbb\xcb\x41\x6c\xc8\x37\xd2\xcc\xf9\x0f\x5a\x16\xed\xe0\xbc\x27\xf0\xe5\x0c\x78\xb2\x19\x8e\x2f\x86\x96\x6b\xa7\x7b\xda\xb5\xfb\xc7\x2b\xf2\xbe\xc6\x73\x7e\x0e\x2f\x69\x7a\x26\xbb\xf6\x0f\xf8\xd6\x42\xb8\x42\xb7\x43\x34\x0c\xc3\xf0\x72\x10\x74\x00\xf4\x31\xf4\x5a\xdb\x57\xe7\x9e\x6e\x1a\xd9\x89\x74\xdf\x24\xe0\x1d\xdf\x10\xc3\xfa\x0c\x77\xe9\x46\xec\x91\x7f\x22\x0c\x15\x80\x75\xec\x3f\x01\x00\x00\xff\xff\x76\x13\x8d\x7e\x49\x11\x00\x00")
