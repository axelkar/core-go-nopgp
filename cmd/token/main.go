package main

import (
	"fmt"
	"os"

	"git.sr.ht/~sircmpwn/core-go/config"
	"git.sr.ht/~sircmpwn/core-go/crypto"
	"git.sr.ht/~sircmpwn/core-go/auth"
)

func main() {
	conf := config.LoadConfig(":1111")
	crypto.InitCrypto(conf)
	tok := auth.DecodeToken(os.Args[1])
	fmt.Printf("%+v\n", tok)
}
