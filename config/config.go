package config

import (
	"log"
	"os"

	"git.sr.ht/~sircmpwn/getopt"
	"github.com/vaughan0/go-ini"

	"git.sr.ht/~sircmpwn/core-go/crypto"
)

var (
	Debug bool
	Addr  string
)

// Loads the application configuration, reads options from the command line,
// and initializes some internals based on these results.
func LoadConfig(defaultAddr string) ini.File {
	Addr = defaultAddr
	var (
		config ini.File
		err    error
	)
	opts, _, err := getopt.Getopts(os.Args, "b:d")
	if err != nil {
		panic(err)
	}

	for _, opt := range opts {
		switch opt.Option {
		case 'b':
			Addr = opt.Value
		case 'd':
			Debug = true
		}
	}

	for _, path := range []string{
		"config.ini",
		"../config.ini",
		"/etc/sr.ht/config.ini",
	} {
		config, err = ini.LoadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		log.Fatalf("Failed to load config file: %v", err)
	}

	crypto.InitCrypto(config)
	return config
}

func GetOrigin(conf ini.File, svc string, external bool) string {
	if external {
		origin, _ := conf.Get(svc, "origin")
		return origin
	}
	origin, ok := conf.Get(svc, "internal-origin")
	if ok {
		return origin
	}
	origin, _ = conf.Get(svc, "origin")
	return origin
}
