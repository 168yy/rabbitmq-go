package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/168yy/rabbitmq-go"
)

func fib(n int) int {
	if n == 0 {
		return 0
	} else if n == 1 {
		return 1
	} else {
		return fib(n-1) + fib(n-2)
	}
}

func failOnError(err error, msg string) {
	if err != nil {
		log.Panicf("%s: %s", msg, err)
	}
}

func main() {
	var (
		conn *rabbitmq.Conn
		err  error
	)

	ctx := context.Background()
	conn, err = rabbitmq.NewConn(
		ctx,
		"amqp://guest:guest@localhost",
		rabbitmq.WithConnectionOptionsLogging,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close(ctx)
	ch, err := conn.GetNewChannel()
	if err != nil {
		return
	}
	defer ch.Close()
	consumer, err := rabbitmq.NewConsumer(
		ctx,
		conn,
		func(ctx context.Context, rw *rabbitmq.ResponseWriter, d rabbitmq.Delivery) rabbitmq.Action {
			bytes, err := json.Marshal(d)
			if err != nil {
				fmt.Println("json.Marshal error:", err.Error())
			}
			fmt.Println("debug msg:", string(bytes))
			log.Printf("consumed: %v", string(d.Body))
			n, err := strconv.Atoi(string(d.Body))
			failOnError(err, "Failed to convert body to integer")

			log.Printf(" [.] fib(%d)", n)
			response := fib(n)
			fmt.Println("response result:", strconv.Itoa(response))
			_, _ = fmt.Fprint(rw, strconv.Itoa(response))
			// rabbitmq.Ack, rabbitmq.NackDiscard, rabbitmq.NackRequeue
			return rabbitmq.Ack
		},
		"rpc_queue",
	)
	if err != nil {
		log.Fatal(err)
	}
	defer consumer.Close(ctx)

	// block main thread - wait for shutdown signal
	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		fmt.Println()
		fmt.Println(sig)
		done <- true
	}()

	fmt.Println("awaiting signal")
	<-done
	fmt.Println("stopping consumer")
}
