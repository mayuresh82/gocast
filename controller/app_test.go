package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAppParsing(t *testing.T) {
	a := assert.New(t)
	app1, err := NewApp("app1", "1.1.1.1/32", []string{"port:tcp:123"}, []string{}, "")
	a.Nil(err)
	app2, err := NewApp("app1", "1.1.1.1/32", []string{"port:tcp:123"}, []string{}, "")
	a.Nil(err)
	app3, err := NewApp("app3", "2.2.2.2/32", []string{"exec:/bin/testme"}, []string{}, "")
	a.Nil(err)

	a.Equal("1.1.1.1/32", app1.Vip.String())
	a.Equal(Monitor_PORT, app1.Monitors[0].Type)
	a.Equal("123", app1.Monitors[0].Port)
	a.Equal("tcp", app1.Monitors[0].Protocol)

	a.Equal(true, app1.Equal(app2))

	a.Equal(Monitor_EXEC, app3.Monitors[0].Type)
	a.Equal("/bin/testme", app3.Monitors[0].Cmd)

	// test errors
	_, err = NewApp("app4", "4.4.4.4", []string{}, []string{}, "")
	a.NotNil(err)

	_, err = NewApp("app4", "4.4.4.4/32", []string{"port:abcd::1023"}, []string{}, "")
	a.NotNil(err)
}
