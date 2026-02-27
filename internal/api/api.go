// Package api provides HTTP API handlers and routing.
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// Engine is the Gin router used by the API server.
var Engine *gin.Engine

// Upgrader upgrades HTTP connections to WebSocket.
var Upgrader = websocket.Upgrader{}
