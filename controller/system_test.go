package controller

import (
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGateway(t *testing.T) {
	execCmd = os.Args[0]
	os.Setenv("test_name", "test_gateway")
	gw, err := gateway()
	assert.Nil(t, err)
	assert.Equal(t, "10.1.1.1", gw.String())
}

func TestVia(t *testing.T) {
	execCmd = os.Args[0]
	os.Setenv("test_name", "test_via")
	ip, err := via(net.ParseIP("10.1.2.100"))
	assert.Nil(t, err)
	assert.Equal(t, "10.1.2.1", ip.String())

	os.Setenv("test_name", "test_via_none")
	ip, err = via(net.ParseIP("10.1.4.1"))
	assert.Nil(t, err)
	assert.Equal(t, "10.1.4.1", ip.String())
}

func TestAddLoopback(t *testing.T) {
	execCmd = os.Args[0]
	os.Setenv("test_name", "test_add_pass")
	_, ipnet, _ := net.ParseCIDR("1.1.1.1/32")
	err := addLoopback("test_app", ipnet)
	assert.Nil(t, err)

	os.Setenv("test_name", "test_add_fail")
	_, ipnet, _ = net.ParseCIDR("1.1.1.1/32")
	err = addLoopback("test_app", ipnet)
	assert.NotNil(t, err)
}

func TestMain(m *testing.M) {
	switch os.Getenv("test_name") {
	case "test_gateway":
		fmt.Println("10.1.1.1")
	case "test_via":
		fmt.Println("10.1.2.1")
	case "test_via_none":
		break
	case "test_add_fail":
		os.Exit(1)
	default:
		break
	}
	if os.Getenv("test_name") != "" {
		return
	}
	os.Exit(m.Run())
}
