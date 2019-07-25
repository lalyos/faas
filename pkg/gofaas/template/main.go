package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/schollz/faas/pkg/gofaas"
	log "github.com/schollz/logger"

	// start generated code
	"github.com/schollz/ingredients"
	// end generated code
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "debug mode")
	flag.Parse()
	if debug {
		log.SetLevel("debug")
	} else {
		log.SetLevel("info")
	}
	log.Infof("running on port %s", "8080")
	http.HandleFunc("/", handler)
	http.ListenAndServe(":8080", nil)
}

func handler(w http.ResponseWriter, r *http.Request) {
	timeStart := time.Now()
	defer func() {
		log.Infof("%s?%s %s", r.URL.Path, r.URL.RawQuery, time.Since(timeStart))
	}()

	response, err := handle(w, r)
	if err != nil {
		res := struct {
			Message string `json:"message"`
			Success bool   `json:"success"`
		}{
			err.Error(),
			false,
		}
		response, _ = json.Marshal(res)
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-Max")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Content-Type", "text/javascript")
	w.Header().Set("Content-Length", strconv.Itoa(len(response)))
	w.Write(response)
}

// start generated code
const functionNameToRun = "NewFromURL"

var paramNames = []string{"url"}

type Input struct {
	Url string `json:"url"`
}

// end generated code

func handle(w http.ResponseWriter, r *http.Request) (response []byte, err error) {
	funcString, ok := r.URL.Query()["func"]
	if !ok {
		err = fmt.Errorf("no func string")
		log.Error(err)
		return
	}

	log.Debug(funcString)
	functionName, jsonBytes, err := gofaas.ParseFunctionString(paramNames, funcString[0])
	if err != nil {
		log.Error(err)
		return
	}

	if functionNameToRun != functionName {
		err = fmt.Errorf("mismatched functions")
		log.Error(err)
		return
	}

	var input Input
	err = json.Unmarshal(jsonBytes, &input)
	if err != nil {
		log.Error(err)
		return
	}

	// start generated code
	out1, out2 := ingredients.NewFromURL(input.Url)
	var b []byte
	responseString := ""

	b, err = json.Marshal(out1)
	if err != nil {
		log.Error(err)
		return
	}
	responseString += `"` + "r" + `"` + ": " + string(b)

	responseString += ","
	b, err = json.Marshal(out2)
	if err != nil {
		log.Error(err)
		return
	}
	responseString += `"` + "err" + `"` + ": " + string(b)
	// end generated code

	responseString = "{" + responseString + "}"
	response = []byte(responseString)
	return
}
