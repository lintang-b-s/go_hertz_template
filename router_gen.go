// Code generated by hertz generator. DO NOT EDIT.

package main

import (
	"github.com/cloudwego/hertz/pkg/app/server"
	router "lintang/go_hertz_template/biz/router"
)

// register registers all routers.
func register(r *server.Hertz) {

	router.GeneratedRegister(r)

	customizedRegister(r)
}
