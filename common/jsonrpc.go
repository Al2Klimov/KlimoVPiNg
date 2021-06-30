package common

type Message struct {
	JsonRpc string      `json:"jsonrpc"`
	Id      interface{} `json:"id"`
}

type Request struct {
	Message `json:",inline"`

	Method string      `json:"method"`
	Params interface{} `json:"params"`
}

type SuccessResponse struct {
	Message `json:",inline"`

	Result interface{} `json:"result"`
}

type ErrorResponse struct {
	Message `json:",inline"`

	Error interface{} `json:"error"`
}

const JsonRpcVersion = "2.0"
