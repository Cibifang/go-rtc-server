// Copyright (C) AlexWoo(Wu Jie) wj19840501@gmail.com
//
// RTC Json SIP

package rtclib

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	simplejson "github.com/bitly/go-simplejson"
	"github.com/go-ini/ini"
)

// return value
const (
	ERROR = iota
	OK
	IGNORE
)

// SIP Request
const (
	UNKNOWN = iota
	INVITE
	ACK
	BYE
	CANCEL
	REGISTER
	OPTIONS
	INFO
	UPDATE
	PRACK
	SUBSCRIBE
	MESSAGE
)

var jsipReqUnparse = map[string]int{
	"INVITE":    INVITE,
	"ACK":       ACK,
	"BYE":       BYE,
	"CANCEL":    CANCEL,
	"REGISTER":  REGISTER,
	"OPTIONS":   OPTIONS,
	"INFO":      INFO,
	"UPDATE":    UPDATE,
	"PRACK":     PRACK,
	"SUBSCRIBE": SUBSCRIBE,
	"MESSAGE":   MESSAGE,
}

var jsipReqParse = map[int]string{
	INVITE:    "INVITE",
	ACK:       "ACK",
	BYE:       "BYE",
	CANCEL:    "CANCEL",
	REGISTER:  "REGISTER",
	OPTIONS:   "OPTIONS",
	INFO:      "INFO",
	UPDATE:    "UPDATE",
	PRACK:     "PRACK",
	SUBSCRIBE: "SUBSCRIBE",
	MESSAGE:   "MESSAGE",
}

var jsipResDesc = map[int]string{
	100: "Trying",
	180: "Ringing",
	181: "Call Is Being Forwarded",
	182: "Queued",
	183: "Session Progress",
	200: "OK",
	202: "Accepted",
	300: "Multiple Choices",
	301: "Moved Permanently",
	302: "Moved Temporarily",
	305: "Use Proxy",
	380: "Alternative Service",
	400: "Bad Request",
	401: "Unauthorized",
	402: "Payment Required",
	403: "Forbidden",
	404: "Not Found",
	405: "Method Not Allowed",
	406: "Not Acceptable",
	407: "Proxy Authentication Required",
	408: "Request Timeout",
	410: "Gone",
	413: "Request Entity Too Large",
	414: "Request-URI Too Large",
	415: "Unsupported Media Type",
	416: "Unsupported URI Scheme",
	420: "Bad Extension",
	421: "Extension Required",
	423: "Interval Too Brief",
	480: "Temporarily not available",
	481: "Call Leg/Transaction Does Not Exist",
	482: "Loop Detected",
	483: "Too Many Hops",
	484: "Address Incomplete",
	485: "Ambiguous",
	486: "Busy Here",
	487: "Request Terminated",
	488: "Not Acceptable Here",
	491: "Request Pending",
	493: "Undecipherable",
	500: "Internal Server Error",
	501: "Not Implemented",
	502: "Bad Gateway",
	503: "Service Unavailable",
	504: "Server Time-out",
	505: "SIP Version not supported",
	513: "Message Too Large",
	600: "Busy Everywhere",
	603: "Decline",
	604: "Does not exist anywhere",
	606: "Not Acceptable",
}

// SIP Transaction
const (
	TRANS_REQ = iota
	TRANS_TRYING
	TRANS_PR
	TRANS_FINALRESP
)

// SIP Session
const (
	INVITE_INIT = iota
	INVITE_REQ
	INVITE_18X
	INVITE_PRACK
	INVITE_UPDATE
	INVITE_200
	INVITE_ACK
	INVITE_REINV
	INVITE_RE200
	INVITE_ERR
	INVITE_END
	INVITE_TERM
)

var jsipInviteState = map[int]string{
	INVITE_INIT:   "INVITE_INIT",
	INVITE_REQ:    "INVITE_REQ",
	INVITE_18X:    "INVITE_18X",
	INVITE_PRACK:  "INVITE_PRACK",
	INVITE_UPDATE: "INVITE_UPDATE",
	INVITE_200:    "INVITE_200",
	INVITE_ACK:    "INVITE_ACK",
	INVITE_REINV:  "INVITE_REINV",
	INVITE_RE200:  "INVITE_RE200",
	INVITE_ERR:    "INVITE_ERR",
	INVITE_END:    "INVITE_END",
	INVITE_TERM:   "INVITE_TERM",
}

const (
	DEFAULT_INIT = iota
	DEFAULT_REQ
	DEFAULT_RESP
)

const (
	RECV = iota
	SEND
)

var jsipDirect = map[int]string{
	RECV: "RECV",
	SEND: "SEND",
}

type JSIP struct {
	Type       int
	Code       int
	RequestURI string
	From       string
	To         string
	CSeq       uint64
	DialogueID string
	Router     []string
	Body       interface{}
	RawMsg     map[string]interface{}

	conn        Conn
	Transaction *JSIPTrasaction
	Session     *JSIPSession
}

type JSIPTrasaction struct {
	Type   int
	State  int
	UAType int
	req    *JSIP
	cseq   uint64

	conn Conn
}

type JSIPSession struct {
	Type    int
	State   int
	UAType  int
	req     *JSIP
	cseq    uint64
	handler func(session *JSIPSession, jsip *JSIP, sendrecv int) int

	conn Conn
}

type JSIPConfig struct {
	Location string `default:"rtc"`
	Realm    string
	Timeout  time.Duration `default:"1s"`
	Qsize    Size_t        `default:"1k"`
}

type JSIPStack struct {
	config     *JSIPConfig
	jsipHandle func(jsip *JSIP)
	log        *Log

	recvq chan *JSIP
	sendq chan *JSIP

	sessions     map[string]*JSIPSession
	transactions map[string]*JSIPTrasaction
}

var jstack *JSIPStack

func (stack *JSIPStack) loadConfig() bool {
	stack.config = new(JSIPConfig)

	confPath := RTCPATH + "/conf/gortc.ini"

	f, err := ini.Load(confPath)
	if err != nil {
		jstack.log.LogError("Load config file %s error: %v", confPath, err)
		return false
	}

	return Config(f, "JSIPStack", stack.config)
}

func (stack *JSIPStack) transactionID(jsip *JSIP, cseq uint64) string {
	return jsip.DialogueID + "_" + strconv.FormatUint(cseq, 10)
}

func (stack *JSIPStack) parseUri(uri string) (string, string) {
	var userWithHost, hostWithPort string

	ss := strings.Split(uri, ";")
	uri = ss[0]

	ss = strings.Split(uri, "@")
	if len(ss) == 1 {
		hostWithPort = ss[0]
	} else {
		hostWithPort = ss[1]
	}

	ss = strings.Split(uri, ":")
	userWithHost = ss[0]

	return userWithHost, hostWithPort
}

func (stack *JSIPStack) connect(uri string) *WSConn {
	userWithHost, hostWithPort := stack.parseUri(uri)
	if userWithHost == "" {
		return nil
	}

	url := "ws://" + hostWithPort + jstack.Location() + "?userid=" +
		jstack.Realm()
	conn := NewWSConn(userWithHost, url, UAC, jstack.Timeout(), jstack.Qsize(),
		RecvMsg)

	conn.Dial()

	return conn
}

// Syntax Check
func (stack *JSIPStack) jsipUnParser(data []byte) (*JSIP, error) {
	json, err := simplejson.NewJson(data)
	if err != nil {
		return nil, err
	}

	jsip := &JSIP{}

	typ, err := json.Get("Type").String()
	if err != nil {
		return nil, errors.New("no Type in jsip msg")
	}

	if typ == "RESPONSE" {
		jsip.Code, err = json.Get("Code").Int()
		if err != nil {
			return nil, errors.New("no Code in jsip response")
		}

		if jsip.Code < 100 || jsip.Code > 699 {
			return nil, fmt.Errorf("unexpected status code %d", jsip.Code)
		}
	} else {
		jsip.Type = jsipReqUnparse[typ]
		if jsip.Type == UNKNOWN {
			return nil, errors.New("unexpected Type")
		}

		jsip.RequestURI, err = json.Get("Request-URI").String()
		if err != nil {
			return nil, errors.New("no Request-URI in jsip request")
		}
	}

	jsip.From, err = json.Get("From").String()
	if err != nil {
		return nil, errors.New("no From in jsip message")
	}

	jsip.To, err = json.Get("To").String()
	if err != nil {
		return nil, errors.New("no To in jsip message")
	}

	jsip.DialogueID, err = json.Get("DialogueID").String()
	if err != nil {
		return nil, errors.New("no DialogueID in jsip message")
	}

	jsip.CSeq, err = json.Get("CSeq").Uint64()
	if err != nil {
		return nil, errors.New("no CSeq in jsip message")
	}

	routers, _ := json.Get("Router").String()
	if routers != "" {
		jsip.Router = strings.Split(routers, ",")
		for i := 0; i < len(jsip.Router); i++ {
			jsip.Router[i] = strings.TrimSpace(jsip.Router[i])
		}
	}

	jsip.RawMsg, err = json.Map()

	jsip.Body = json.Get("Body")

	return jsip, nil
}

func (stack *JSIPStack) jsipPrepared(jsip *JSIP) (*JSIP, error) {
	if jsip.DialogueID == "" {
		return nil, errors.New("DialogueID not set")
	}

	if jsipReqParse[jsip.Type] == "" {
		return nil, errors.New("Unknown message type")
	}

	if jsip.Code != 0 && (jsip.Code < 100 || jsip.Code > 699) {
		return nil, fmt.Errorf("Unknown response %s", jsip.Name())
	}

	if jsip.From == "" {
		return nil, errors.New("From not set")
	}

	if jsip.To == "" {
		return nil, errors.New("To not set")
	}

	if jsip.Code == 0 {
		if jsip.RequestURI == "" {
			return nil, errors.New("RequestURI not set in JSIP Request")
		}
	} else {
		if jsip.CSeq == 0 {
			return nil, errors.New("CSeq not set in JSIP Response")
		}
	}

	if jsip.RawMsg == nil {
		jsip.RawMsg = make(map[string]interface{})
	}

	if jsip.Code != 0 {
		jsip.RawMsg["Type"] = "RESPONSE"
		jsip.RawMsg["Code"] = jsip.Code
		jsip.RawMsg["Desc"] = jsipResDesc[jsip.Code]
		jsip.RawMsg["CSeq"] = jsip.CSeq
	} else {
		jsip.RawMsg["Type"] = jsipReqParse[jsip.Type]
		jsip.RawMsg["Request-URI"] = jsip.RequestURI
	}

	jsip.RawMsg["From"] = jsip.From
	jsip.RawMsg["To"] = jsip.To
	jsip.RawMsg["DialogueID"] = jsip.DialogueID

	if len(jsip.Router) > 0 {
		router := jsip.Router[0]
		for i := 1; i < len(jsip.Router); i++ {
			router += ", " + jsip.Router[i]
		}
		jsip.RawMsg["Router"] = router
	}

	if jsip.Body != nil {
		jsip.RawMsg["Body"] = jsip.Body
	}

	return jsip, nil
}

// Transaction Layer
func (stack *JSIPStack) jsipTrasaction(jsip *JSIP, sendrecv int) int {
	tid := stack.transactionID(jsip, jsip.CSeq)
	trans := stack.transactions[tid]
	jsip.Transaction = trans

	if trans == nil { // Request
		if jsip.Code != 0 {
			stack.log.LogError("process %s but trans is nil", jsip.Name())
			return ERROR
		}

		trans = &JSIPTrasaction{
			Type:  jsip.Type,
			State: TRANS_REQ,
			req:   jsip,
			cseq:  jsip.CSeq,
		}

		if sendrecv == RECV {
			trans.UAType = UAS
			trans.conn = jsip.conn
		} else {
			trans.UAType = UAC
		}

		stack.transactions[tid] = trans
		jsip.Transaction = trans

		if jsip.Type == ACK {
			delete(stack.transactions, tid)

			relatedid, ok := jsip.RawMsg["RelatedID"]
			if !ok {
				stack.log.LogInfo("ACK miss RelatedID")
				return IGNORE
			}

			rid, _ := strconv.ParseUint(string(relatedid.(json.Number)), 10, 64)
			tid = stack.transactionID(jsip, rid)
			ackTrans := stack.transactions[tid]
			if ackTrans == nil {
				stack.log.LogInfo("Transaction INVITE not exist")
				return IGNORE
			}

			delete(stack.transactions, tid)
		}

		if jsip.Type == CANCEL {
			relatedid, ok := jsip.RawMsg["RelatedID"]
			if !ok {
				stack.log.LogInfo("CANCEL miss RelatedID")
				return IGNORE
			}

			rid, _ := strconv.ParseUint(string(relatedid.(json.Number)), 10, 64)
			tid = stack.transactionID(jsip, rid)
			cancelTrans := stack.transactions[tid]
			if cancelTrans == nil {
				stack.log.LogInfo("Transaction Cancelled not exist")
				return IGNORE
			}

			if cancelTrans.State == TRANS_FINALRESP {
				stack.log.LogInfo("Transaction in finalize response, cannot cancel")
				return IGNORE
			}

			if sendrecv == RECV {
				// Send CANCLE 200
				SendMsg(JSIPMsgRes(jsip, 200))
				// Send Req 487
				SendMsg(JSIPMsgRes(cancelTrans.req, 487))
			}
		}

		if jsip.Type == BYE {
			if sendrecv == RECV {
				// Send BYE 200
				SendMsg(JSIPMsgRes(jsip, 200))
			}
		}

		return OK
	}

	if jsip.Code == 0 {
		stack.log.LogError("process %s but trans exist", jsip.Name())
		return ERROR
	}

	// Response
	if trans.UAType == UAS && sendrecv == RECV ||
		trans.UAType == UAC && sendrecv == SEND {

		stack.log.LogError("Response direct is same as Request direct")
		return ERROR
	}

	jsip.Type = trans.Type

	if jsip.Code == 100 {
		if trans.State > TRANS_TRYING {
			stack.log.LogError("process 100 Trying but state is %d", trans.State)
			return ERROR
		}

		trans.State = TRANS_TRYING

		return IGNORE
	}

	if jsip.Code < 200 && jsip.Code > 100 {
		if trans.State > TRANS_PR {
			stack.log.LogError("process %s but state is %d", jsip.Name(),
				trans.State)
			return ERROR
		}

		trans.State = TRANS_PR

		return OK
	}

	if trans.State == TRANS_FINALRESP {
		stack.log.LogError("process %s but state is %d", jsip.Name(),
			trans.State)
		return ERROR
	}

	trans.State = TRANS_FINALRESP

	if trans.Type != INVITE {
		delete(stack.transactions, tid)
	} else {
		if jsip.Code >= 300 && sendrecv == RECV {
			// Send Ack for INVITE 3XX 4XX 5XX 6XX Response
			SendMsg(JSIPMsgAck(jsip))
		}
	}

	if trans.Type == CANCEL && sendrecv == RECV {
		// Ignore CANCEL 200 received
		return IGNORE
	}

	if trans.Type == BYE && sendrecv == RECV {
		// Ignore BYE 200 received
		return IGNORE
	}

	return OK
}

// Session Layer
func (stack *JSIPStack) jsipInviteSession(session *JSIPSession, jsip *JSIP,
	sendrecv int) int {

	if jsip.Type == INFO {
		return OK
	}

	if jsip.Type == CANCEL && jsip.Code > 0 {
		return OK
	}

	switch session.State {
	case INVITE_INIT:
		if jsip.Type == INVITE && jsip.Code == 0 {
			session.State = INVITE_REQ
			return OK
		}

	case INVITE_REQ:
		switch jsip.Type {
		case CANCEL:
			return OK
		case INVITE:
			switch {
			case jsip.Code < 200 && jsip.Code > 100:
				session.State = INVITE_18X
				return OK
			case jsip.Code == 200:
				session.State = INVITE_200
				return OK
			case jsip.Code >= 300:
				session.State = INVITE_ERR
				return OK
			}
		}
	case INVITE_18X:
		switch jsip.Type {
		case CANCEL:
			return OK
		case INVITE:
			switch {
			case jsip.Code < 200 && jsip.Code > 100:
				return OK
			case jsip.Code == 200:
				session.State = INVITE_200
				return OK
			case jsip.Code >= 300:
				session.State = INVITE_ERR
				return OK
			}
		case PRACK:
			if jsip.Code == 0 && sendrecv == session.UAType {
				session.State = INVITE_PRACK
				return OK
			}
		case UPDATE:
			if jsip.Code == 0 {
				session.State = INVITE_UPDATE
				return OK
			}
		}
	case INVITE_PRACK:
		if jsip.Code == 200 && jsip.Type == PRACK {
			session.State = INVITE_18X
			return OK
		}
	case INVITE_UPDATE:
		if jsip.Code == 200 && jsip.Type == UPDATE {
			session.State = INVITE_18X
			return OK
		}
	case INVITE_200:
		if jsip.Type == ACK {
			session.State = INVITE_ACK
			return OK
		} else if jsip.Type == BYE {
			session.State = INVITE_END
			return OK
		}
	case INVITE_ACK:
		switch {
		case jsip.Type == INVITE:
			if jsip.Code == 0 {
				session.State = INVITE_REINV
				return OK
			}
		case jsip.Type == UPDATE:
			if jsip.Code == 0 {
				SendMsg(JSIPMsgRes(jsip, 200))
				return IGNORE
			}

			if jsip.Code == 200 {
				if sendrecv == SEND {
					return OK
				} else {
					return IGNORE
				}
			}
		case jsip.Type == BYE:
			session.State = INVITE_END
			return OK
		case jsip.Type == INFO: // INFO and INFO 200
			return OK
		}
	case INVITE_REINV:
		if jsip.Code == 200 && jsip.Type == INVITE {
			session.State = INVITE_RE200
			return OK
		} else if jsip.Type == BYE {
			session.State = INVITE_END
			return OK
		}
	case INVITE_RE200:
		if jsip.Type == ACK {
			session.State = INVITE_ACK
			return OK
		} else if jsip.Type == BYE {
			session.State = INVITE_END
			return OK
		}
	case INVITE_ERR:
		if jsip.Type == ACK { // ERR ACK
			session.State = INVITE_TERM
			return IGNORE
		}
	case INVITE_END:
		if jsip.Type == BYE && jsip.Code > 0 {
			session.State = INVITE_TERM
			return OK
		}
	}

	stack.log.LogError("%s %s in %s", jsipDirect[sendrecv], jsip.Name(),
		jsipInviteState[session.State])

	return ERROR
}

func (stack *JSIPStack) jsipDefaultSession(session *JSIPSession, jsip *JSIP,
	sendrecv int) int {

	if jsip.Type == CANCEL {
		return OK
	}

	switch session.State {
	case DEFAULT_INIT:
		if jsip.Code != 0 {
			stack.log.LogError("Recv response %s but session state is DEFAULT_INIT",
				jsip.Name())
			return ERROR
		}

		if session.Type != INVITE && session.Type != REGISTER &&
			session.Type != OPTIONS && session.Type != MESSAGE &&
			session.Type != SUBSCRIBE {

			stack.log.LogError("Session not exist when process msg %s", jsip.Name())
			session.State = DEFAULT_REQ
			if sendrecv == RECV {
				SendMsg(JSIPMsgRes(jsip, 481))
			}
			return ERROR
		}

		session.State = DEFAULT_REQ

	case DEFAULT_REQ:
		if jsip.Code == 0 {
			stack.log.LogError("Recv request %s but session state is DEFAULT_REQ",
				jsip.Name())
			return ERROR
		}

		if jsip.Code >= 200 {
			session.State = DEFAULT_RESP
		}
	}

	return OK
}

func (stack *JSIPStack) jsipSession(jsip *JSIP, sendrecv int) int {
	session := stack.sessions[jsip.DialogueID]
	jsip.Session = session

	if session == nil {
		if jsip.Code != 0 {
			stack.log.LogError("recv response but session is nil")
			return IGNORE
		}

		session = &JSIPSession{
			Type: jsip.Type,
			req:  jsip,
		}

		if sendrecv == RECV {
			session.UAType = UAS
			session.conn = jsip.conn
		} else {
			session.UAType = UAC
		}

		stack.sessions[jsip.DialogueID] = session
		jsip.Session = session

		switch session.Type {
		case INVITE:
			session.handler = stack.jsipInviteSession
		default:
			session.handler = stack.jsipDefaultSession
		}
	}

	if jsip.Code == 0 {
		if sendrecv == RECV {
			session.cseq = jsip.CSeq
		} else {
			session.cseq++
			jsip.CSeq = session.cseq
			jsip.RawMsg["CSeq"] = jsip.CSeq
		}
	}

	ret := session.handler(session, jsip, sendrecv)

	if session.Type == INVITE {
		if session.State == INVITE_TERM {
			delete(stack.sessions, jsip.DialogueID)
		}
	} else {
		if session.State == DEFAULT_RESP {
			delete(stack.sessions, jsip.DialogueID)
		}
	}

	return ret
}

func (stack *JSIPStack) recvJSIPMsg(jsip *JSIP) {
	// Transaction Layer
	ret := stack.jsipTrasaction(jsip, RECV)
	if ret == ERROR {
		return
	} else if ret == IGNORE {
		return
	}

	// Session Layer
	ret = stack.jsipSession(jsip, RECV)
	if ret == ERROR {
		return
	} else if ret == IGNORE {
		return
	}

	stack.jsipHandle(jsip)
}

func (stack *JSIPStack) sendJSIPMsg(jsip *JSIP) {
	// Session Layer
	ret := stack.jsipSession(jsip, SEND)
	if ret == ERROR {
		return
	} else if ret == IGNORE {
		return
	}

	// Transaction Layer
	ret = stack.jsipTrasaction(jsip, SEND)
	if ret == ERROR {
		return
	} else if ret == IGNORE {
		return
	}

	if jsip.Transaction.conn != nil {
		jsip.conn = jsip.Transaction.conn
	} else {
		jsip.conn = jsip.Session.conn
	}

	if jsip.conn == nil {
		var conn *WSConn
		if len(jsip.Router) > 0 {
			conn = stack.connect(jsip.Router[0])
		} else {
			conn = stack.connect(jsip.RequestURI)
		}

		if conn == nil {
			//TODO Error
			return
		}

		jsip.conn = conn
		jsip.Transaction.conn = conn
		jsip.Session.conn = conn
	}

	data, _ := json.Marshal(jsip.RawMsg)
	jsip.conn.Send(data)
}

func (stack *JSIPStack) run() {
	for {
		select {
		case jsip := <-stack.recvq:
			stack.recvJSIPMsg(jsip)
			fmt.Println("Recv:", jsip.Abstract())
		case jsip := <-stack.sendq:
			stack.sendJSIPMsg(jsip)
			fmt.Println("Send:", jsip.Abstract())
		}
	}
}

// Init JSIP Stack
func InitJSIPStack(h func(jsip *JSIP), log *Log) *JSIPStack {
	jstack = &JSIPStack{
		jsipHandle:   h,
		log:          log,
		sessions:     make(map[string]*JSIPSession),
		transactions: make(map[string]*JSIPTrasaction),
	}

	if !jstack.loadConfig() {
		return nil
	}

	if jstack.config.Realm == "" {
		jstack.log.LogError("JSIPStack Realm not configured")
		return nil
	}

	jstack.recvq = make(chan *JSIP, jstack.Qsize())
	jstack.sendq = make(chan *JSIP, jstack.Qsize())

	go jstack.run()

	return jstack
}

// JStack Config
func (stack *JSIPStack) Location() string {
	return stack.config.Location
}

func (stack *JSIPStack) Realm() string {
	return stack.config.Realm
}

func (stack *JSIPStack) Timeout() time.Duration {
	return stack.config.Timeout
}

func (stack *JSIPStack) Qsize() uint64 {
	return uint64(stack.config.Qsize)
}

// JSIP interface
func (jsip *JSIP) Name() string {
	req := jsipReqParse[jsip.Type]
	if req == "" {
		req = "UNKNOWN"
	}

	if jsip.Code == 0 {
		return req
	} else {
		code := strconv.Itoa(jsip.Code)
		return req + "_" + code
	}
}

func (jsip *JSIP) Abstract() string {
	abstract := jsip.Name()
	if jsip.Code == 0 {
		abstract += " RequestURI: " + jsip.RequestURI
	}
	abstract += " From: " + jsip.From + " To: " + jsip.To + " CSeq: " +
		strconv.Itoa(int(jsip.CSeq)) + " DialogueID: " + jsip.DialogueID

	if len(jsip.Router) > 0 {
		abstract += " Router: " + jsip.Router[0]
		for i := 1; i < len(jsip.Router); i++ {
			abstract += "," + jsip.Router[0]
		}
	}

	return abstract
}

func (jsip *JSIP) Detail() string {
	data, _ := json.Marshal(jsip.RawMsg)
	detail := jsip.Name() + ": " + string(data)

	return detail
}

// interface
func JSIPMsgClone(req *JSIP, dlg string) *JSIP {
	msg := &JSIP{
		Type:       req.Type,
		Code:       req.Code,
		RequestURI: req.RequestURI,
		From:       req.From,
		To:         req.To,
		CSeq:       req.CSeq,
		DialogueID: dlg,
		Router:     req.Router,
		Body:       req.Body,
		RawMsg:     req.RawMsg,
	}

	return msg
}

func JSIPMsgRes(req *JSIP, code int) *JSIP {
	if req.Code != 0 {
		fmt.Println("Cannot send response for response")
		return nil
	}

	resp := &JSIP{
		Type:       req.Type,
		Code:       code,
		From:       req.From,
		To:         req.To,
		CSeq:       req.CSeq,
		DialogueID: req.DialogueID,
		RawMsg:     make(map[string]interface{}),

		conn:        req.conn,
		Transaction: req.Transaction,
		Session:     req.Session,
	}

	return resp
}

func JSIPMsgAck(resp *JSIP) *JSIP {
	ack := &JSIP{
		Type:       ACK,
		RequestURI: resp.Transaction.req.RequestURI,
		From:       resp.From,
		To:         resp.To,
		DialogueID: resp.DialogueID,
		RawMsg:     make(map[string]interface{}),

		conn:    resp.conn,
		Session: resp.Session,
	}

	ack.RawMsg["RelatedID"] = resp.CSeq

	return ack
}

func JSIPMsgBye(session *JSIPSession) *JSIP {
	bye := &JSIP{
		Type:       BYE,
		RequestURI: session.req.RequestURI,
		From:       session.req.From,
		To:         session.req.To,
		DialogueID: session.req.DialogueID,

		conn:    session.conn,
		Session: session,
	}

	return bye
}

func JSIPMsgUpdate(session *JSIPSession) *JSIP {
	update := &JSIP{
		Type:       UPDATE,
		RequestURI: session.req.RequestURI,
		From:       session.req.From,
		To:         session.req.To,
		DialogueID: session.req.DialogueID,

		conn:    session.conn,
		Session: session,
	}

	return update
}

func JSIPMsgCancel(trans *JSIPTrasaction) *JSIP {
	cancel := &JSIP{
		Type:       CANCEL,
		RequestURI: trans.req.RequestURI,
		From:       trans.req.From,
		To:         trans.req.To,
		DialogueID: trans.req.DialogueID,

		conn:    trans.conn,
		Session: trans.req.Session,
	}

	cancel.RawMsg["RelatedID"] = trans.cseq

	return cancel
}

func RecvMsg(conn Conn, data []byte) {
	jsip, err := jstack.jsipUnParser(data)
	if err != nil {
		fmt.Println("------- parse error", err)
		return
	}

	jsip.conn = conn

	jstack.recvq <- jsip
}

func SendMsg(j *JSIP) {
	jsip, err := jstack.jsipPrepared(j)
	if err != nil {
		fmt.Println("------- prepared error", err)
		return
	}

	jstack.sendq <- jsip
}
