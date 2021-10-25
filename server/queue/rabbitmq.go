package queue

import (
	"encoding/json"
	"fmt"
	"github.com/streadway/amqp"
	"io/ioutil"
	"log"
	"os"
)

type MsgData struct {
	Key   string
	Value string
}

type RabbitMQ struct {
	Channel     *amqp.Channel
	Connection  *amqp.Connection
	Declaration *RabbitMQDeclaration
}

type RabbitMQTopic struct {
	Name     string
	Exchange string
}

type RabbitMQQueue struct {
	Name   string
	Topics []RabbitMQTopic
}

type RabbitMQDeclaration struct {
	Exchanges []string
	Queues    []RabbitMQQueue
}

func NewRabbitMQ() *RabbitMQ {
	return &RabbitMQ{}
}

func (q *RabbitMQ) Init() {
	rabbitmqURL := os.Getenv("RABBITMQ_URL")
	fmt.Println("Intit rabbit", rabbitmqURL)
	conn, err := amqp.Dial(rabbitmqURL)
	if err != nil {
		log.Fatal(err)
	}

	ch, err := conn.Channel()
	if err != nil {
		log.Fatal(err)
	}

	var dec RabbitMQDeclaration

	decs, err := ioutil.ReadFile("./queue/queue_declaration.json")
	if err != nil {
		log.Fatalln(err)
	}

	if err = json.Unmarshal(decs, &dec); err != nil {
		log.Fatalln(err)
	}

	// Exchange
	for _, exName := range dec.Exchanges {
		// ExchangeDeclare: name, type, durable, autoDelete, internal, noWait, args
		err = ch.ExchangeDeclare(exName, "topic", true, false, false, false, nil)
		if err != nil {
			log.Fatal(err)
		}
	}

	// Queue
	for _, q := range dec.Queues {
		// QueueDeclare: name, durable, autoDelete, exclusive, noWait, argss
		_, err := ch.QueueDeclare(q.Name, true, false, false, false, nil)
		if err != nil {
			log.Fatal(err)
		}

		// Binding
		for _, t := range q.Topics {
			// QueueBind: queue name, routing key, exchange, noWait, argss
			err = ch.QueueBind(q.Name, t.Name, t.Exchange, false, nil)
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	q.Declaration = &dec
	q.Channel = ch
	q.Connection = conn

	log.Println("RabbitMQ initialized susscessfully")

}

// PushMessageToTopic:
func (q *RabbitMQ) PushMessageToTopic(exchange, routing string, data MsgData) (err error) {
	bytes, _ := json.Marshal(data)
	return q.pushMessage(exchange, routing, bytes)
}

// PushRawMessage
func (q *RabbitMQ) PushRawMessage(exchange, routing string, data []byte) (err error) {
	return q.pushMessage(exchange, routing, data)
}

func (q *RabbitMQ) pushMessage(exchange, routing string, data []byte) (err error) {
	err = q.Channel.Publish(
		exchange, // exchange
		routing,  // routing key
		false,    // mandatory
		false,    // immediate
		amqp.Publishing{
			ContentType:  "text/plain",
			Body:         data,
			DeliveryMode: amqp.Persistent,
		})
	return err
}

// Consume :
func (q *RabbitMQ) Consume() (chan amqp.Delivery, chan error) {
	chanMsg := make(chan amqp.Delivery)
	chanErr := make(chan error)

	for _, queue := range q.Declaration.Queues {
		go func(qName string) {
			// Consume: queue, consumer, autoAck, exclusive, noLocal, noWait, args
			msgs, err := q.Channel.Consume(qName, "", false, false, false, false, nil)
			if err != nil {
				chanErr <- err
			}

			forever := make(chan bool)

			go func() {
				for d := range msgs {
					chanMsg <- d
				}
			}()
			<-forever
		}(queue.Name)
	}

	return chanMsg, chanErr
}
