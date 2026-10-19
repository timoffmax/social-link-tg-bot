package request

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
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

// apiURLTemplate is the Bot API URL requests are sent to; tests point it at
// an httptest server.
var apiURLTemplate = API_URL

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
	// Timeout, when set, caps this call below ClientTimeout.
	Timeout time.Duration `json:"-"`
}

// ResponseInfo describes Telegram's answer. StatusCode is 400 for any answer
// with ok=false and 0 when there was no answer; HTTPStatus is the answer's
// real HTTP status (e.g. 429, 502) and RetryAfter the seconds Telegram asks
// to wait after a 429.
type ResponseInfo struct {
	StatusCode int         `json:"status_code"`
	HTTPStatus int         `json:"http_status"`
	RetryAfter int         `json:"retry_after"`
	Message    string      `json:"message"`
	Payload    interface{} `json:"payload"`
}

type AbstractTgPayload struct {
	Ok          bool                `json:"ok"`
	Description string              `json:"description"`
	Parameters  *ResponseParameters `json:"parameters"`
	Result      interface{}         `json:"result"`
}

// ResponseParameters are the parameters of a failed answer.
type ResponseParameters struct {
	RetryAfter int `json:"retry_after"`
}

// ErrNotSent wraps the error of a request that never left the bot (it could
// not be built), so Telegram can not have received it.
var ErrNotSent = errors.New("request not sent")

// ClientTimeout caps one Telegram API call, from sending the request to
// reading the answer.
const ClientTimeout = 120 * time.Second

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
	err = WithoutRequestURL(r.ApiMethod, err)

	if nil != err {
		log.Printf("❌ [TG-API] %s not sent: %v", r.ApiMethod, err)
		err = fmt.Errorf("%w: %w", ErrNotSent, err)
	}

	if nil == err {
		var cancel context.CancelFunc
		request, cancel = withTimeout(request, r.Timeout)
		defer cancel()
		response, sendErr := sendRequest(request)
		sendErr = WithoutRequestURL(r.ApiMethod, sendErr)

		if nil != response {
			defer response.Body.Close()
		}

		if nil == sendErr {
			var readErr error
			result, readErr = parseResponse(response, nil, &respPayload)
			sendErr = answerReadError(r.ApiMethod, readErr)
		}

		// A transport failure (timeout, dropped connection, an answer cut off
		// before its end) leaves StatusCode 0, since Telegram may or may not
		// have received the request.
		if nil != sendErr {
			log.Printf("❌ [TG-API] %s failed without a response: %v", r.ApiMethod, sendErr)
			err = sendErr
		}

		// Return error for non-200 status codes
		if (nil == sendErr) && (200 != result.StatusCode) {
			log.Printf("❌ [TG-API] %s failed: status=%d description=%q", r.ApiMethod, result.StatusCode, result.Message)
			err = fmt.Errorf("API error: %d - %s", result.StatusCode, result.Message)
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

	if nil == err {
		request.Header.Set(HeaderContentType, r.RequestContentType)
	}

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
			fieldValue := multipartFieldValue(key, value)
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

// formFieldChatID is the multipart field written without scientific notation.
const formFieldChatID = "chat_id"

// multipartFieldValue returns a payload value as multipart text: objects and
// arrays (reply_markup, a media group's media) as the JSON the Bot API
// expects, chat_id as a plain integer, everything else as text.
func multipartFieldValue(key string, value interface{}) string {
	result := fmt.Sprintf("%v", value)
	floatValue, isFloat := value.(float64)

	if (key == formFieldChatID) && (isFloat == true) {
		result = fmt.Sprintf("%.0f", floatValue)
	}

	if isJSONFormValue(value) == true {
		result = jsonFormValue(key, value)
	}

	return result
}

// isJSONFormValue reports an object or array decoded from the payload JSON.
func isJSONFormValue(value interface{}) bool {
	result := false

	switch value.(type) {
	case map[string]interface{}, []interface{}:
		result = true
	}

	return result
}

// jsonFormValue returns a multipart field value as JSON; an encoding failure
// is logged, since the field would reach Telegram empty.
func jsonFormValue(key string, value interface{}) string {
	encoded, err := json.Marshal(value)

	if err != nil {
		log.Printf("❌ [TG-API] encoding multipart field %s failed: %v", key, err)
	}

	result := string(encoded)

	return result
}

// SetAPIURLTemplate changes the Bot API URL template (with the {{token}} and
// {{method_name}} placeholders) and returns the previous one, so tests can
// send requests to an httptest server and restore it afterwards.
func SetAPIURLTemplate(template string) string {
	result := apiURLTemplate
	apiURLTemplate = template

	return result
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
	result := strings.Replace(apiURLTemplate, "{{token}}", r.Token, 1)
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
	Timeout: ClientTimeout,
	Transport: &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	},
}

// withTimeout bounds a request by timeout; 0 leaves only ClientTimeout.
func withTimeout(httpRequest *http.Request, timeout time.Duration) (*http.Request, context.CancelFunc) {
	ctx := httpRequest.Context()
	cancel := context.CancelFunc(func() {})

	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}

	result := httpRequest.WithContext(ctx)

	return result, cancel
}

func sendRequest(r *http.Request) (*http.Response, error) {
	return telegramHTTPClient.Do(r)
}

/*
FIll the response general structure with the received response information
*/
func parseResponse(response *http.Response, error error, respPayload *interface{}) (ResponseInfo, error) {
	result := ResponseInfo{}
	tgPayload, readErr := bodyToTgPayload(response, respPayload)

	result.StatusCode = response.StatusCode
	result.HTTPStatus = response.StatusCode
	result.RetryAfter = retryAfter(tgPayload)

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

	// An answer cut off before its end is no answer: StatusCode stays 0.
	if nil != readErr {
		result = ResponseInfo{HTTPStatus: response.StatusCode}
	}

	return result, readErr
}

// answerReadError names the method of an answer that could not be read to
// its end (timeout, dropped connection); nil when it was read.
func answerReadError(apiMethod string, readErr error) error {
	var result error

	if nil != readErr {
		result = fmt.Errorf("%s answer cut off: %w", apiMethod, readErr)
	}

	return result
}

// retryAfter returns the seconds a failed answer asks to wait, 0 for none.
func retryAfter(tgPayload AbstractTgPayload) int {
	result := 0

	if nil != tgPayload.Parameters {
		result = tgPayload.Parameters.RetryAfter
	}

	return result
}

/*
Parses response body like a standard JSON Telegram response; the error is a
failure to read the body to its end
*/
func bodyToTgPayload(response *http.Response, respPayload *interface{}) (AbstractTgPayload, error) {
	result := AbstractTgPayload{
		Result: respPayload,
	}

	body, readErr := ioutil.ReadAll(response.Body)
	err := json.Unmarshal(body, &result)

	if nil != err {
		result.Result = body
	}

	return result, readErr
}
