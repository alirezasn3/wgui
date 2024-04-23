package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
)

type Log struct {
	Time          int64     `json:"time" bson:"time"`
	Level         string    `json:"level" bson:"level"`
	Message       string    `json:"msg" bson:"msg"`
	Peer          string    `json:"peer" bson:"peer"`
	PublicAddress string    `json:"publicAddress" bson:"publicAddress"`
	ExpireAt      time.Time `json:"-" bson:"expireAt"`
}

type CustomWriter struct {
	W          io.Writer
	Collection *mongo.Collection
}

func (e CustomWriter) Write(p []byte) (int, error) {
	go func() {
		var l Log
		err := json.Unmarshal(p, &l)
		if err != nil {
			fmt.Println(err)
			return
		}

		// logs will be removed from db after 2 days
		l.ExpireAt = time.Now().Add(time.Hour * 48)

		_, err = e.Collection.InsertOne(context.TODO(), l)
		if err != nil {
			fmt.Println(err)
		}
	}()
	return fmt.Println(string(p))
}
