package pbx

import (
    "testing"
    "reflect"
)


// Test generated using Keploy
func TestAuthLevelEnum(t *testing.T) {
    level := AuthLevel_AUTH
    enumPtr := level.Enum()
    if *enumPtr != AuthLevel_AUTH {
        t.Errorf("Expected %v, got %v", AuthLevel_AUTH, *enumPtr)
    }
}

// Test generated using Keploy
func TestInfoNoteString(t *testing.T) {
    tests := []struct {
        input    InfoNote
        expected string
    }{
        {InfoNote_X1, "X1"},
        {InfoNote_READ, "READ"},
        {InfoNote_RECV, "RECV"},
        {InfoNote_KP, "KP"},
        {InfoNote_CALL, "CALL"},
    }

    for _, test := range tests {
        result := test.input.String()
        if result != test.expected {
            t.Errorf("For input %v, expected %v, got %v", test.input, test.expected, result)
        }
    }
}


// Test generated using Keploy
func TestDefaultAcsModeReset(t *testing.T) {
    mode := &DefaultAcsMode{
        Auth: "auth_value",
        Anon: "anon_value",
    }
    mode.Reset()
    if mode.Auth != "" || mode.Anon != "" {
        t.Errorf("Expected Auth and Anon to be empty strings, got Auth: %v, Anon: %v", mode.Auth, mode.Anon)
    }
}


// Test generated using Keploy
func TestSetDescGetPublic(t *testing.T) {
    publicData := []byte("public_data")
    desc := &SetDesc{Public: publicData}
    result := desc.GetPublic()
    if string(result) != string(publicData) {
        t.Errorf("Expected %v, got %v", string(publicData), string(result))
    }
}


// Test generated using Keploy
func TestClientAccGetCred(t *testing.T) {
    creds := []*ClientCred{
        {Method: "email", Value: "user@example.com"},
        {Method: "tel", Value: "+1234567890"},
    }
    acc := &ClientAcc{Cred: creds}
    result := acc.GetCred()
    if len(result) != len(creds) {
        t.Errorf("Expected %d credentials, got %d", len(creds), len(result))
    }
    for i, cred := range result {
        if cred.Method != creds[i].Method || cred.Value != creds[i].Value {
            t.Errorf("Expected credential %v, got %v", creds[i], cred)
        }
    }
}


// Test generated using Keploy
func TestServerDataGetSeqId(t *testing.T) {
    seqID := int32(42)
    data := &ServerData{SeqId: seqID}
    result := data.GetSeqId()
    if result != seqID {
        t.Errorf("Expected %v, got %v", seqID, result)
    }
}


// Test generated using Keploy
func TestAccessModeGetters(t *testing.T) {
    acs := &AccessMode{
        Want: "RW",
        Given: "R",
    }
    if acs.GetWant() != "RW" {
        t.Errorf("Expected Want to be 'RW', got '%v'", acs.GetWant())
    }
    if acs.GetGiven() != "R" {
        t.Errorf("Expected Given to be 'R', got '%v'", acs.GetGiven())
    }
}


// Test generated using Keploy
func TestTopicDescGetSeqId(t *testing.T) {
    desc := &TopicDesc{
        SeqId: 123,
    }
    if desc.GetSeqId() != 123 {
        t.Errorf("Expected SeqId to be %d, got %d", 123, desc.GetSeqId())
    }
}


// Test generated using Keploy
func TestServerDataGetContent(t *testing.T) {
    contentData := []byte("test content")
    data := &ServerData{
        Content: contentData,
    }
    result := data.GetContent()
    if string(result) != string(contentData) {
        t.Errorf("Expected content '%s', got '%s'", contentData, result)
    }
}


// Test generated using Keploy
func TestClientExtraGetAuthLevel(t *testing.T) {
    extra := &ClientExtra{
        AuthLevel: AuthLevel_AUTH,
    }
    if extra.GetAuthLevel() != AuthLevel_AUTH {
        t.Errorf("Expected AuthLevel %v, got %v", AuthLevel_AUTH, extra.GetAuthLevel())
    }
}


// Test generated using Keploy
func TestServerCtrlGetCode(t *testing.T) {
    ctrl := &ServerCtrl{
        Code: 200,
    }
    if ctrl.GetCode() != 200 {
        t.Errorf("Expected Code to be %d, got %d", 200, ctrl.GetCode())
    }
}


// Test generated using Keploy
func TestClientGetMethods(t *testing.T) {
    query := &GetQuery{
        What: "desc",
    }
    clientGet := &ClientGet{
        Id:    "test-id",
        Topic: "test-topic",
        Query: query,
    }

    if clientGet.GetId() != "test-id" {
        t.Errorf("Expected Id to be 'test-id', got '%v'", clientGet.GetId())
    }
    if clientGet.GetTopic() != "test-topic" {
        t.Errorf("Expected Topic to be 'test-topic', got '%v'", clientGet.GetTopic())
    }
    if clientGet.GetQuery() != query {
        t.Errorf("Expected Query to be %v, got %v", query, clientGet.GetQuery())
    }
}


// Test generated using Keploy
func TestClientCredMethods(t *testing.T) {
    params := map[string][]byte{
        "param1": []byte("value1"),
    }
    cred := &ClientCred{
        Method:   "email",
        Value:    "test@example.com",
        Response: "response",
        Params:   params,
    }

    if cred.GetMethod() != "email" {
        t.Errorf("Expected Method to be 'email', got '%v'", cred.GetMethod())
    }
    if cred.GetValue() != "test@example.com" {
        t.Errorf("Expected Value to be 'test@example.com', got '%v'", cred.GetValue())
    }
    if cred.GetResponse() != "response" {
        t.Errorf("Expected Response to be 'response', got '%v'", cred.GetResponse())
    }
    if !reflect.DeepEqual(cred.GetParams(), params) {
        t.Errorf("Expected Params to be %v, got %v", params, cred.GetParams())
    }
}


// Test generated using Keploy
func TestClientHiGetUserAgent(t *testing.T) {
    clientHi := &ClientHi{
        UserAgent: "TestAgent",
    }
    if clientHi.GetUserAgent() != "TestAgent" {
        t.Errorf("Expected UserAgent to be 'TestAgent', got '%v'", clientHi.GetUserAgent())
    }
}


// Test generated using Keploy
func TestClientLoginMethods(t *testing.T) {
    creds := []*ClientCred{
        {Method: "email", Value: "user@example.com"},
    }
    login := &ClientLogin{
        Id:     "test-id",
        Scheme: "basic",
        Secret: []byte("secret"),
        Cred:   creds,
    }

    if login.GetId() != "test-id" {
        t.Errorf("Expected Id to be 'test-id', got '%v'", login.GetId())
    }
    if login.GetScheme() != "basic" {
        t.Errorf("Expected Scheme to be 'basic', got '%v'", login.GetScheme())
    }
    if string(login.GetSecret()) != "secret" {
        t.Errorf("Expected Secret to be 'secret', got '%v'", string(login.GetSecret()))
    }
    if !reflect.DeepEqual(login.GetCred(), creds) {
        t.Errorf("Expected Cred to be %v, got %v", creds, login.GetCred())
    }
}


// Test generated using Keploy
func TestServerCtrlMethods(t *testing.T) {
    params := map[string][]byte{
        "param1": []byte("value1"),
    }
    ctrl := &ServerCtrl{
        Code:   200,
        Id:     "test-id",
        Text:   "OK",
        Params: params,
    }

    if ctrl.GetCode() != 200 {
        t.Errorf("Expected Code to be 200, got %d", ctrl.GetCode())
    }
    if ctrl.GetId() != "test-id" {
        t.Errorf("Expected Id to be 'test-id', got '%v'", ctrl.GetId())
    }
    if ctrl.GetText() != "OK" {
        t.Errorf("Expected Text to be 'OK', got '%v'", ctrl.GetText())
    }
    if !reflect.DeepEqual(ctrl.GetParams(), params) {
        t.Errorf("Expected Params to be %v, got %v", params, ctrl.GetParams())
    }
}


// Test generated using Keploy
func TestClientNoteGetTopic(t *testing.T) {
    note := &ClientNote{Topic: "test-topic"}
    if note.GetTopic() != "test-topic" {
        t.Errorf("Expected Topic to be 'test-topic', got '%v'", note.GetTopic())
    }
}


// Test generated using Keploy
func TestClientNoteGetWhat(t *testing.T) {
    note := &ClientNote{What: InfoNote_READ}
    if note.GetWhat() != InfoNote_READ {
        t.Errorf("Expected What to be InfoNote_READ, got %v", note.GetWhat())
    }
}

