package main

import (
	"math/big"

	"github.com/c2h5oh/hide"
	"github.com/sirupsen/logrus"
)

func init() {
	i, _ := new(big.Int).SetString("5463458053", 10)
	if err := hide.Default.SetInt64(i); err != nil {
		logrus.Fatal(err)
	}
	i, _ = new(big.Int).SetString("3267000013", 10)
	if err := hide.Default.SetXor(i); err != nil {
		logrus.Fatal(err)
	}
}
