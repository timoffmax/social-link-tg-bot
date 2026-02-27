package request

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/google/go-querystring/query"
	"github.com/timoffmax/social-link-bot/config"
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

type MediaGroupPayload interface {
	GetFilePaths() []string
	GetFileField() string
}

// mediaAdapter implements MediaPayload interface for dynamic payloads
type mediaAdapter struct {
	filePath  string
	fileField string
}

func (m *mediaAdapter) GetFilePath() string {
	return m.filePath
}

func (m *mediaAdapter) GetFileField() string {
	return m.fileField
}

func (r *AuthorizedRequest) Send(respPayload interface{}) (ResponseInfo, error) {
	var result ResponseInfo
	request, err := prepareRequest(r)

	if nil == err {
		response, err := sendRequest(request)
		defer response.Body.Close()

		result = parseResponse(response, err, &respPayload)
		
		// Return error for non-200 status codes
		if 200 != result.StatusCode {
			return result, fmt.Errorf("API error: %d - %s", result.StatusCode, result.Message)
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
	var isMediaGroupPayload bool
	
	// Use reflection to check for required methods since interface assertion fails across package boundaries
	payloadValue := reflect.ValueOf(r.Payload)
	payloadType := payloadValue.Type()
	
	// For struct values with pointer receiver methods, we need to create a pointer
	if payloadValue.Kind() == reflect.Struct {
		// Create a new pointer to the struct value
		newPtr := reflect.New(payloadType)
		newPtr.Elem().Set(payloadValue)
		payloadValue = newPtr
		payloadType = payloadValue.Type()
	}
	
	// Check for media group payload first (multiple files)
	getFilePathsMethod := payloadValue.MethodByName("GetFilePaths")
	getFileFieldMethod := payloadValue.MethodByName("GetFileField")
	
	if getFilePathsMethod.IsValid() && getFileFieldMethod.IsValid() {
		// This is a media group payload
		filePathsResult := getFilePathsMethod.Call(nil)
		fileFieldResult := getFileFieldMethod.Call(nil)
		
		if len(filePathsResult) > 0 && len(fileFieldResult) > 0 {
			filePaths := filePathsResult[0]
			
			isMediaGroupPayload = true
			
			// Add multiple files to form with attach:// naming
			for i := 0; i < filePaths.Len(); i++ {
				filePath := filePaths.Index(i).String()
				
				file, err := os.Open(filePath)
				if err != nil {
					return nil, err
				}
				defer file.Close()

				// Use photo0, photo1, etc. as the field names matching attach://photo0 in JSON
				fileFieldName := fmt.Sprintf("photo%d", i)
				fileWriter, err := writer.CreateFormFile(fileFieldName, filepath.Base(filePath))
				if err != nil {
					return nil, err
				}

				_, err = io.Copy(fileWriter, file)
				if err != nil {
					return nil, err
				}
			}
		}
	} else {
		// Check for single file payload
		getFilePathMethod := payloadValue.MethodByName("GetFilePath")
		
		if getFilePathMethod.IsValid() && getFileFieldMethod.IsValid() {
			// Call the methods using reflection
			filePathResult := getFilePathMethod.Call(nil)
			fileFieldResult := getFileFieldMethod.Call(nil)
			
			if len(filePathResult) > 0 && len(fileFieldResult) > 0 {
				filePath := filePathResult[0].String()
				fieldName := fileFieldResult[0].String()
				
				isMediaPayload = true
				
				// Create an adapter to implement MediaPayload interface
				mediaPayload = &mediaAdapter{filePath: filePath, fileField: fieldName}
			
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
		}
	}
	// Add other fields as form data
	payloadBytes, _ := json.Marshal(r.Payload)
	var payloadMap map[string]interface{}
	json.Unmarshal(payloadBytes, &payloadMap)

	for key, value := range payloadMap {
		// Skip file fields that are handled separately
		skipField := false
		if isMediaPayload && key == mediaPayload.GetFileField() {
			skipField = true
		}
		if isMediaGroupPayload && (key == "FilePaths" || key == "file_paths") {
			skipField = true
		}
		
		if !skipField && value != nil && fmt.Sprintf("%v", value) != "" {
			var fieldValue string
			
			// Special handling for chat_id to avoid scientific notation
			if key == "chat_id" {
				if floatVal, ok := value.(float64); ok {
					fieldValue = fmt.Sprintf("%.0f", floatVal)
				} else {
					fieldValue = fmt.Sprintf("%v", value)
				}
			} else if key == "media" && isMediaGroupPayload {
				// For media groups, we need to serialize the media array as JSON
				mediaBytes, _ := json.Marshal(value)
				fieldValue = string(mediaBytes)
			} else {
				fieldValue = fmt.Sprintf("%v", value)
			}
			
			writer.WriteField(key, fieldValue)
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

// telegramHTTPClient is a shared HTTP client for all Telegram API calls.
// Reusing a single client enables TCP connection pooling and prevents hanging requests.
var telegramHTTPClient = &http.Client{
	Timeout: 120 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	},
}

func sendRequest(r *http.Request) (*http.Response, error) {
	return telegramHTTPClient.Do(r)
}

/*
FIll the response general structure with the received response information
*/
func parseResponse(response *http.Response, error error, respPayload *interface{}) ResponseInfo {
	result := ResponseInfo{}
	tgPayload := bodyToTgPayload(response, respPayload)

	result.StatusCode = response.StatusCode

	// Check Telegram API's 'ok' field for API-level errors
	if !tgPayload.Ok {
		// Telegram API returned an error, treat as 400 status
		result.StatusCode = 400
		if "" != tgPayload.Description {
			result.Message = tgPayload.Description
		} else {
			result.Message = "Telegram API error"
		}
	} else if "" != tgPayload.Description {
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
