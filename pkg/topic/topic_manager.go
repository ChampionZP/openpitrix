// Copyright 2018 The OpenPitrix Authors. All rights reserved.
// Use of this source code is governed by a Apache license
// that can be found in the LICENSE file.

package topic

import (
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"openpitrix.io/openpitrix/pkg/etcd"
	"openpitrix.io/openpitrix/pkg/logger"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// TODO: get allowed host from global config
		return true
	},
}

type receiversT map[*websocket.Conn]*sync.Mutex

type topicManager struct {
	*etcd.Etcd
	receiverMap map[string]receiversT
	addReceiver chan receiver
	delReceiver chan receiver
	msgChan     chan userMessage
}

func NewTopicManager(e *etcd.Etcd) *topicManager {
	var tm topicManager
	tm.Etcd = e
	tm.msgChan = watchEvents(e)
	tm.addReceiver = make(chan receiver, 255)
	tm.delReceiver = make(chan receiver, 255)
	tm.receiverMap = make(map[string]receiversT)
	return &tm
}

func (tm *topicManager) getReceivers(userId string) receiversT {
	rs, ok := tm.receiverMap[userId]
	if !ok {
		rs = make(receiversT)
		tm.receiverMap[userId] = rs
	}
	return rs
}

func (tm *topicManager) Run() {
	//go func() {
	//	c := time.Tick(2 * time.Second)
	//	for range c {
	//		for userId := range tm.receiverMap {
	//			msg := Message{
	//				Type: Create,
	//			}
	//			pushEvent(tm.Pi.Etcd, userId, msg)
	//			logger.Debug("Got user [%s], send msg: %+v", userId, msg)
	//		}
	//	}
	//}()
	for {
		select {
		case receiver := <-tm.addReceiver:
			receivers := tm.getReceivers(receiver.UserId)
			receivers[receiver.Conn] = &sync.Mutex{}

		case receiver := <-tm.delReceiver:
			receivers := tm.getReceivers(receiver.UserId)

			delete(receivers, receiver.Conn)
			if len(receivers) == 0 {
				delete(tm.receiverMap, receiver.UserId)
			}
			go receiver.Conn.Close()

		case userMsg := <-tm.msgChan:
			receivers := tm.getReceivers(userMsg.UserId)
			for r, mutex := range receivers {
				go writeMessage(r, mutex, userMsg)
			}
		}
	}
}

func writeMessage(conn *websocket.Conn, mutex *sync.Mutex, userMsg userMessage) {
	mutex.Lock()
	defer mutex.Unlock()
	err := conn.WriteJSON(userMsg.Message)
	if err != nil {
		logger.Error("Failed to send message [%+v] to [%+v], error: %+v", userMsg, conn, err)
	}
	logger.Debug("Message sent [%+v]", userMsg)
}

func (tm *topicManager) HandleEvent(w http.ResponseWriter, r *http.Request) {
	// TODO: check sid
	userId := r.URL.Query().Get("uid")
	if userId == "" {
		http.Error(w, "Unauthorized: uid is required.", http.StatusUnauthorized)
		return
	}
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Info("Upgrade websocket request failed: %+v", err)
		return
	}
	receiver := receiver{UserId: userId, Conn: c}
	tm.addReceiver <- receiver
	for {
		_, _, err := receiver.Conn.ReadMessage()
		if err != nil {
			tm.delReceiver <- receiver
			logger.Error("Connection [%p] error: %+v", receiver.Conn, err)
			return
		}
	}
}
