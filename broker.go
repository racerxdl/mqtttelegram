package main

import (
	"encoding/json"
	"fmt"
	"github.com/eclipse/paho.mqtt.golang"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/quan-to/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	telegramBotToken = os.Getenv("telegram_bot_token")
	telegramAdminId  = os.Getenv("telegram_admin")
	groupToTopic     = os.Getenv("group_to_topic")
	mqttHost         = os.Getenv("mqtt_server")
)

var telLog = slog.Scope("Telegram")
var mqttLog = slog.Scope("MQTT")

var groupMaps = map[int64]string{}
var topicMaps = map[string]int64{}
var topicToMap = map[string]string{}

var telegramBot *tgbotapi.BotAPI
var mqttClient mqtt.Client

func doMessage(topic string, jsonData []byte) {
	defer func() {
		if r := recover(); r != nil {
			mqttLog.Error("Recovered from panic on doMessage.")
			mqttClient.Publish(fmt.Sprintf("%s_error", topic), 0, false, fmt.Sprintf("There was an error processing the message: recovered from panic"))
		}
	}()

	var data map[string]interface{}
	err := json.Unmarshal(jsonData, &data)
	if err != nil {
		mqttLog.Error("Received invalid JSON: %s", err)
		mqttClient.Publish(fmt.Sprintf("%s_error", topic), 0, false, fmt.Sprintf("There was an error processing the message: %s", err))
		return
	}

	t := data["type"].(string)

	if t == "message" {
		group, ok := topicMaps[topic]
		if !ok {
			mqttLog.Warn("Received message on topic %s but no telegram channel associated.", topic)
			return
		}

		if data["message"] != nil {
			from := "Unknown"
			if data["from"] != nil {
				from = data["from"].(string)
			}
			message := data["message"].(string)
			mqttLog.Info("[%d] %s: %s", group, from, message)

			msg := tgbotapi.NewMessage(group, fmt.Sprintf("*%s*: %s", from, message))
			msg.ParseMode = tgbotapi.ModeMarkdown

			_, err := telegramBot.Send(msg)
			if err != nil {
				telLog.Error("Error sending message to group %d: %s", group, err)
			}
		} else {
			mqttLog.Error("Received data without message: %s", string(jsonData))
			mqttClient.Publish(fmt.Sprintf("%s_error", topic), 0, false, fmt.Sprintf("Received data without message: %s", string(jsonData)))
		}
	} else {
		mqttLog.Info("Received message (%s): %s", t, string(jsonData))
	}
}

func CheckTelegramUpdates() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates, err := telegramBot.GetUpdatesChan(u)

	if err != nil {
		telLog.Error("Error fetching updates: %s", err)
		return
	}

	for update := range updates {
		if update.ChannelPost != nil {
			msg := update.ChannelPost

			from := msg.Chat.Title
			telLog.Info("%s: %s", from, msg.Text)

			topic, ok := groupMaps[msg.Chat.ID]

			if ok {
				topicTo, ok := topicToMap[topic]
				telLog.Debug("Redirecting message from Channel: %s", msg.Chat.Title)
				if ok {

					data := map[string]interface{}{
						"sendmsg": true,
						"to":      topicTo,
						"message": msg.Text,
					}

					jsonData, _ := json.Marshal(data)
					mqttLog.Debug("Publishing to %s_msg: %s", topic, string(jsonData))
					mqttClient.Publish(fmt.Sprintf("%s_msg", topic), 0, false, jsonData)
				} else {
					telLog.Error("Received message but can't send because no msgToName defined!")
				}
			}
		}

		if update.Message != nil { // ignore any non-Message Updates
			msg := update.Message

			from := msg.From.UserName
			if from == "" {
				from = "Unknown"
			}

			if msg.Chat.ID != int64(msg.From.ID) {
				telLog.Info("[%s(%d)] %s: %s", msg.Chat.Title, msg.Chat.ID, from, msg.Text)
			} else {
				telLog.Info("%s: %s", from, msg.Text)
			}

			topic, ok := groupMaps[msg.Chat.ID]

			if ok {
				topicTo, ok := topicToMap[topic]
				telLog.Debug("Redirecting message from User: %s", msg.Chat.Title)
				if ok {

					data := map[string]interface{}{
						"sendmsg": true,
						"to":      topicTo,
						"message": fmt.Sprintf("%s %s: %s", msg.From.FirstName, msg.From.LastName, msg.Text),
					}

					jsonData, _ := json.Marshal(data)
					mqttLog.Debug("Publishing to %s_msg: %s", topic, string(jsonData))
					mqttClient.Publish(fmt.Sprintf("%s_msg", topic), 0, false, jsonData)
				} else {
					telLog.Error("Received message but can't send because no msgToName defined!")
				}
			}
		}
	}
}

func main() {
	var err error

	if telegramBotToken == "" {
		slog.Error("Telegram Bot Token was not defined! Please define at environment variable \"telegram_bot_token\"")
	}

	if mqttHost == "" {
		slog.Error(`MQTT Server was not defined! Please define at environment variable 'mqtt_server'`)
	}

	if telegramAdminId == "" {
		slog.Warn(`Telegram Administrator ID not defined. Administrator will be disabled. Define at environment variable 'telegram_admin'`)
	}

	if groupToTopic == "" {
		slog.Error(`Group to Topic was not defined! Please define at environment variable 'group_to_topic'`)
		slog.Warn(`Format: groupId:mqttTopic:messageTo;groupId2:mqttTopic2:messageTo2`)
	}

	if telegramBotToken == "" || groupToTopic == "" || mqttHost == "" {
		slog.Fatal("One or more environment variables not defined. Aborting...")
	}

	groups := strings.Split(groupToTopic, ";")

	for _, m := range groups {
		z := strings.Split(m, ":")
		group, _ := strconv.ParseInt(z[0], 10, 64)
		topic := z[1]

		mqttLog.Info("Mapping Telegram Group %d to MQTT Topic %s", group, topic)

		groupMaps[group] = topic
		topicMaps[topic] = group

		if len(z) > 2 {
			topicToMap[topic] = z[2]
		} else {
			mqttLog.Warn("Topic %s does not have a third argument which represents the message to.")
		}
	}

	slog.Info("Starting")
	// region Telegram Bot Connect
	telegramBot, err = tgbotapi.NewBotAPI(telegramBotToken)
	if err != nil {
		telLog.Fatal(err)
	}

	telegramBot.Debug = true

	telLog.Info("Authorized on account %s", telegramBot.Self.UserName)
	// endregion
	// region MQTT
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:1883", mqttHost))
	opts.SetDefaultPublishHandler(func(client mqtt.Client, message mqtt.Message) {
		mqttLog.Debug(`Received Message on Topic %s: %s`, message.Topic(), string(message.Payload()))
		doMessage(message.Topic(), message.Payload())
	})
	opts.SetPingTimeout(1 * time.Second)
	opts.SetKeepAlive(2 * time.Second)

	mqttClient = mqtt.NewClient(opts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		mqttLog.Fatal(token.Error())
	}

	mqttLog.Info("Connected")

	token := mqttClient.Subscribe("presence", 0, nil)
	token.Wait()
	err = token.Error()
	if err != nil {
		mqttLog.Fatal("Error subscribing to %s: %s", "presence", err)
	}

	for k := range topicMaps {
		token := mqttClient.Subscribe(k, 0, nil)
		token.Wait()
		err = token.Error()
		if err != nil {
			mqttLog.Fatal("Error subscribing to %s: %s", k, err)
		}
	}
	// endregion

	c := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)

	go func() {
		sig := <-c
		slog.Warn("Received Signal %d", sig)
		done <- true
	}()

	tick := time.NewTicker(time.Second * 1)
	running := true

	slog.Info("Starting global loop")

	for running {
		select {
		case <-tick.C:
			CheckTelegramUpdates()
		case <-done:
			running = false
		}
	}
	slog.Info("MQTT Telegram Stopped")
}
