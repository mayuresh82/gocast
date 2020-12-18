package controller

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPortMonitor(t *testing.T) {
	a := assert.New(t)
	addr, _ := net.ResolveTCPAddr("tcp", ":33333")
	conn, err := net.ListenTCP("tcp", addr)
	if err != nil {
		a.FailNow(err.Error())
	}
	a.True(portMonitor("tcp", "33333"))
	a.False(portMonitor("tcp", "44444"))
	conn.Close()

	uaddr, _ := net.ResolveUDPAddr("udp", ":33333")
	udpconn, err := net.ListenUDP("udp", uaddr)
	if err != nil {
		a.FailNow(err.Error())
	}
	a.True(portMonitor("udp", "33333"))
	a.False(portMonitor("udp", "44444"))
	udpconn.Close()
}

func TestExecMonitor(t *testing.T) {
	a := assert.New(t)
	a.True(execMonitor("echo foo"))
	a.False(execMonitor("echo foo && false"))
}
