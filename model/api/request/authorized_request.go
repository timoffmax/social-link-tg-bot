package request

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/google/go-querystring/query"
	"github.com/timoffmax/social-link-bot/config"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const API_URL = "https://api.telegram.org/bot{{token}}/{{method_name}}"

const ContentTypeFormUrlEncoded = "application/x-www-form-urlencoded"
const ContentTypeJson = "application/json; charset=UTF-8"
const ContentTypeFormData = "multipart/form-data"

const RequestMethodGET = "GET"
const RequestMethodPOST = "POST"

const HeaderContentType = "Content-Type"

type AuthorizedRequest struct {
	Token              string      `json:"token"`
	ApiMethod          string      `json:"api_method"`
	RequestMethod      string      `json:"request_method"`
	RequestContentType string      `json:"request_content_type"`
	Payload            interface{} `json:"payload"`
}

type ResponseInfo struct {
	StatusCode int         `json:"status_code"`
	Message    string      `json:"message"`
	Payload    interface{} `json:"payload"`
}

type AbstractTgPayload struct {
	Ok          bool        `json:"ok"`
	Description string      `json:"description"`
	Result      interface{} `json:"result"`
}

type MediaPayload interface {
	GetFilePath() string
	GetFileField() string
}

func (r *AuthorizedRequest) Send(respPayload interface{}) (ResponseInfo, error) {
	var result ResponseInfo
	request, err := prepareRequest(r)

	if nil == err {
		response, err := sendRequest(request)
		defer response.Body.Close()

		result := parseResponse(response, err, &respPayload)

		// TODO: Use a logger
		if 200 != result.StatusCode {
			fmt.Println("Response error: ", result.StatusCode, " - ", result.Message)
		}
	}

	return result, err
}

/*
Creates the request and prepares its body and parameters
*/
func prepareRequest(r *AuthorizedRequest) (*http.Request, error) {
	authorizeRequest(r)

	requestURL := prepareRequestURL(r)
	
	if r.RequestContentType == ContentTypeFormData {
		return prepareMultipartRequest(r, requestURL)
	}
	
	jsonPayload, _ := json.Marshal(r.Payload)
	request, err := http.NewRequest(r.RequestMethod, requestURL, bytes.NewBuffer(jsonPayload))
	request.Header.Set(HeaderContentType, r.RequestContentType)

	return request, err
}

/*
Creates multipart form data request for file uploads
*/
func prepareMultipartRequest(r *AuthorizedRequest, requestURL string) (*http.Request, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	var mediaPayload MediaPayload
	var isMediaPayload bool
	
	// Check if payload implements MediaPayload interface
	if mediaPayload, isMediaPayload = r.Payload.(MediaPayload); isMediaPayload {
		filePath := mediaPayload.GetFilePath()
		fieldName := mediaPayload.GetFileField()
		
		// Add file to form
		file, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		fileWriter, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
		if err != nil {
			return nil, err
		}

		_, err = io.Copy(fileWriter, file)
		if err != nil {
			return nil, err
		}
	}

	// Add other fields as form data
	payloadBytes, _ := json.Marshal(r.Payload)
	var payloadMap map[string]interface{}
	json.Unmarshal(payloadBytes, &payloadMap)

	for key, value := range payloadMap {
		if !isMediaPayload || key != mediaPayload.GetFileField() {
			if value != nil && fmt.Sprintf("%v", value) != "" {
				writer.WriteField(key, fmt.Sprintf("%v", value))
			}
		}
	}

	writer.Close()

	request, err := http.NewRequest(r.RequestMethod, requestURL, &buf)
	if err != nil {
		return nil, err
	}

	request.Header.Set(HeaderContentType, writer.FormDataContentType())
	return request, nil
}

/*
Sets auth parameters
*/
func authorizeRequest(r *AuthorizedRequest) {
	r.Token = config.GetTgBotToken()

	if "" == r.Token {
		panic("Can't make a request: Telegram bot token is not set")
	}
}

/*
Substitutes URL placeholders, adds GET parameters
*/
func prepareRequestURL(r *AuthorizedRequest) string {
	result := strings.Replace(API_URL, "{{token}}", r.Token, 1)
	result = strings.Replace(result, "{{method_name}}", r.ApiMethod, 1)

	if (RequestMethodGET == r.RequestMethod) && (nil != r.Payload) {
		payload := r.Payload

		urlParams, _ := query.Values(payload)
		paramsString := urlParams.Encode()
		result += "?" + paramsString
	}

	return result
}

/*
Sends the request and returns the response
*/
func sendRequest(r *http.Request) (*http.Response, error) {
	client := &http.Client{}
	return client.Do(r)
}

/*
FIll the response general structure with the received response information
*/
func parseResponse(response *http.Response, error error, respPayload *interface{}) ResponseInfo {
	result := ResponseInfo{}
	tgPayload := bodyToTgPayload(response, respPayload)

	result.StatusCode = response.StatusCode

	if "" != tgPayload.Description {
		result.Message = tgPayload.Description
	}

	if nil != error {
		result.Message = error.Error()
	}

	return result
}

/*
Parses response body like a standard JSON Telegram response
*/
func bodyToTgPayload(response *http.Response, respPayload *interface{}) AbstractTgPayload {
	result := AbstractTgPayload{
		Result: respPayload,
	}

	body, _ := ioutil.ReadAll(response.Body)
	err := json.Unmarshal(body, &result)

	if nil != err {
		result.Result = body
	}

	return result
}
