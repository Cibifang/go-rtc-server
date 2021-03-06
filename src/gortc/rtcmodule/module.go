// Copyright (C) AlexWoo(Wu Jie) wj19840501@gmail.com
//
// RTC Module

package rtcmodule

import (
	"net/http"
	"os"
	"rtclib"
	"strings"

	"github.com/go-ini/ini"
	"github.com/gorilla/websocket"
)

type RTCModuleConfig struct {
	LogLevel      string
	LogRotateSize rtclib.Size_t
	Listen        string
	TlsListen     string
	Cert          string
	Key           string
}

type RTCModule struct {
	config    *RTCModuleConfig
	server    *http.Server
	tlsServer *http.Server

	jsipC  chan *rtclib.JSIP
	taskQ  chan *rtclib.Task
	jstack *rtclib.JSIPStack
}

var module *RTCModule

func NewRTCModule() *RTCModule {
	module = &RTCModule{}

	return module
}

func (m *RTCModule) LoadConfig() bool {
	m.config = new(RTCModuleConfig)

	confPath := rtclib.RTCPATH + "/conf/gortc.ini"

	f, err := ini.Load(confPath)
	if err != nil {
		LogError("Load config file %s error: %v", confPath, err)
		return false
	}

	return rtclib.Config(f, "RTCModule", m.config)
}

func wsCheckOrigin(r *http.Request) bool {
	//Access Control from here
	return true
}

func (m *RTCModule) handler(w http.ResponseWriter, req *http.Request) {
	userid := req.URL.Query().Get("userid")
	if userid == "" {
		LogError("Miss userid")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  64 * 1024,
		WriteBufferSize: 64 * 1024,
		CheckOrigin:     wsCheckOrigin,
	}

	c, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		LogError("Create Websocket server failed, %v", err)
		return
	}

	conn := rtclib.NewWSConn(userid, "", rtclib.UAS, m.jstack.Timeout(),
		m.jstack.Qsize(), rtclib.RecvMsg)

	conn.Accept(c)
}

func (m *RTCModule) Init() bool {
	initLog(m.config)

	if !initSLPM() {
		LogError("SLP Manager init error")
		return false
	}

	m.jsipC = make(chan *rtclib.JSIP, 4096)
	m.taskQ = make(chan *rtclib.Task, 1024)

	m.jstack = rtclib.InitJSIPStack(m.jsipC, log, rtclib.RTCPATH)
	if m.jstack == nil {
		LogError("JSIP Stack init error")
		return false
	}

	serveMux := &http.ServeMux{}
	serveMux.HandleFunc(m.jstack.Location(), m.handler)

	if m.config.Listen != "" {
		m.server = &http.Server{Addr: m.config.Listen, Handler: serveMux}
	}

	if m.config.TlsListen != "" {
		if m.config.Cert == "" || m.config.Key == "" {
			LogError("TLS cert(%s) or key(%s) file configured error",
				m.config.Cert, m.config.Key)
			return false
		}

		m.config.Cert = rtclib.RTCPATH + "/certs/" + m.config.Cert

		_, err := os.Stat(m.config.Cert)
		if err != nil {
			LogError("TLS cert(%s) error: %v", m.config.Cert, err)
			return false
		}

		m.config.Key = rtclib.RTCPATH + "/certs/" + m.config.Key

		_, err = os.Stat(m.config.Key)
		if err != nil {
			LogError("TLS cert(%s) error: %v", m.config.Key, err)
			return false
		}

		m.tlsServer = &http.Server{Addr: m.config.TlsListen, Handler: serveMux}
	}

	return true
}

func (m *RTCModule) processMsg(jsip *rtclib.JSIP) {
	dlg := jsip.DialogueID
	t := rtclib.GetTask(dlg)
	if t != nil {
		t.OnMsg(jsip)
		return
	}

	if jsip.Code > 0 {
		LogError("Receive %s but SLP if finished", jsip.Name())
		return
	}

	if jsip.Type != rtclib.INVITE && jsip.Type != rtclib.REGISTER &&
		jsip.Type != rtclib.OPTIONS && jsip.Type != rtclib.MESSAGE &&
		jsip.Type != rtclib.SUBSCRIBE {

		LogError("Receive %s but SLP if finished", jsip.Name())
		return
	}

	slpname := "default"

	if len(jsip.Router) != 0 {
		router0 := jsip.Router[0]
		paras := strings.Split(router0, ";")[1:]

		for _, para := range paras {
			if strings.HasPrefix(para, "type=") {
				ss := strings.SplitN(para, "=", 2)
				if ss[1] != "" {
					slpname = ss[1]
				}
			}
		}
	}

	t = rtclib.NewTask(dlg, m.taskQ)
	t.Name = slpname
	getSLP(t, SLPPROCESS)
	if t.SLP == nil {
		rtclib.SendMsg(rtclib.JSIPMsgRes(jsip, 404))
		t.DelTask()
		return
	}

	t.OnMsg(jsip)
}

func (m *RTCModule) process() {
	for {
		select {
		case jsip := <-m.jsipC:
			m.processMsg(jsip)
		case task := <-m.taskQ:
			task.DelTask()
		}
	}
}

func (m *RTCModule) Run() {
	wait := 0
	if m.server != nil {
		wait++
	}
	if m.tlsServer != nil {
		wait++
	}
	quit := make(chan bool, wait)

	if m.server != nil {
		LogInfo("RTCServer start ...")
		go func() {
			//TODO retry
			err := m.server.ListenAndServe()
			LogError("RTCServer quit, %v", err)
			quit <- true
		}()
	}

	if m.tlsServer != nil {
		LogInfo("RTCServer TLS start ...")
		go func() {
			err := m.tlsServer.ListenAndServeTLS(m.config.Cert, m.config.Key)
			LogError("RTCServer TLS quit, %v", err)
			quit <- true
		}()
	}

	go m.process()

	for {
		<-quit
		wait--

		if wait == 0 {
			break
		}
	}
}

func (m *RTCModule) Exit() {
	if m.server != nil {
		LogInfo("close RTCServer ...")
		m.server.Close()
	}

	if m.tlsServer != nil {
		LogInfo("close RTCServer TLS ...")
		m.tlsServer.Close()
	}
}
