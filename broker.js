#!/usr/bin/env nodejs

// IRC to MQTT Message Redirection
// Copyright (C) 2016  Lucas Teske

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

const { QLog, undefinedOrNull } = require('quanto-commons');

const mqtt = require('mqtt')
const TelegramBot = require('node-telegram-bot-api');

const telegramBotToken = process.env['telegram_bot_token'];
const defaultGroupId = !undefinedOrNull(process.env['telegram_group_id']) ? parseInt(process.env['telegram_group_id'], 10) : null;
const mqttTopic = process.env['mqtt_topic'];
const mqttHost = process.env['mqtt_server'];
const telegramAdminId = process.env['telegram_admin'];
const msgToName = process.env['mqtt_message_to'];

const GlobalLog = QLog.scope('Global');
const TelLog = QLog.scope('Telegram');
const MQTTLog = QLog.scope('MQTT');

GlobalLog.headPadding = 30;
TelLog.headPadding = 30;
MQTTLog.headPadding = 30;

// region Variable Check
if (undefinedOrNull(telegramBotToken)) {
  GlobalLog.error(`Telegram Bot Token was not defined! Please define at environment variable ${'telegram_bot_token'.warn.bold}`);
}
if (undefinedOrNull(defaultGroupId)) {
  GlobalLog.error(`Telegram Group ID was not defined! Please define at environment variable ${'telegram_group_id'.warn.bold}`);
}
if (undefinedOrNull(mqttTopic)) {
  GlobalLog.error(`MQTT Topic was not defined! Please define at environment variable ${'mqtt_topic'.warn.bold}`);
}
if (undefinedOrNull(mqttHost)) {
  GlobalLog.error(`MQTT Server was not defined! Please define at environment variable ${'mqtt_server'.warn.bold}`);
}
if (undefinedOrNull(telegramAdminId)) {
  GlobalLog.warn(`Telegram Administrator ID not defined. Administrator will be disabled. Define at environment variable ${'telegram_admin'.warn.bold}`);
}

if (undefinedOrNull(telegramBotToken) || undefinedOrNull(defaultGroupId) || undefinedOrNull(mqttTopic) || undefinedOrNull(mqttHost)) {
  GlobalLog.fatal('One or more environment variables not defined. Aborting...');
  process.exit(1);
}

// endregion

const bot = new TelegramBot(telegramBotToken, { polling: true });
const client  = mqtt.connect(`mqtt://${mqttHost}`)

client.on('connect', function () {
  MQTTLog.success('Connected');
  client.subscribe('presence')
  client.subscribe(mqttTopic);
})

const opts = { parse_mode: 'Markdown' };

const doMessage = (topic, jsonData) => {
  try {
    const data = JSON.parse(jsonData);
    if (data.type === 'message') {
      if (!undefinedOrNull(data.message)) {
        MQTTLog.info(`${(data.from||'Unknown').warn.bold}: ${data.message}`);
        bot.sendMessage(defaultGroupId, `*${data.from}*: ${data.message}`, opts);
      } else {
        MQTTLog.error(`Received Data without message: ${jsonData}`);
        client.publish(`${mqttTopic}_error`, 'Received a payload without message.');
      }
    } else {
      MQTTLog.info(`Receive ${data.type} message. ${jsonData}`);
    }
  } catch(e) {
    client.publish(`${mqttTopic}_error`, `There was an error parsing message from ${topic}: ${e}`);
  }
}

client.on('message', function (topic, message) {
  MQTTLog.debug(`Received Message on Topic ${topic}: ${message}`);
  if (topic === `${mqttTopic}`) {
    doMessage(topic, message);
  } else {
    MQTTLog.note(`${topic}: ${message}`);
  }
});

bot.on('message', function (msg) {
  // TelLog.debug(JSON.stringify(msg, null, 2));
  if (msg.chat.id !== msg.from.id) {
    TelLog.note(`[${msg.chat.title.white}(${msg.chat.id.toString().gray})] ${(msg.from.username || 'Unknown').warn.bold}: ${msg.text}`);
  } else {
    TelLog.note(`${(msg.from.username || 'Unknown').warn.bold}: ${msg.text}`);
  }

  if (msg.chat.id == defaultGroupId && !undefinedOrNull(msg.text)) {
    TelLog.debug(`Redirecting message from Channel: ${msg.chat.title.white.bold}`);
    if (!undefinedOrNull(msgToName)) {
      client.publish(`${mqttTopic}_msg`, JSON.stringify({
        "sendmsg": true,
        "to": msgToName,
        "message": `${msg.from.first_name} ${msg.from.last_name}: ${msg.text}`,
      }));
    } else {
      TelLog.error('Received message but can\'t send because no msgToName defined!');
    }
  }
});

bot.on('channel_post', function (msg) {
  if (msg.chat.id == defaultGroupId) {
    TelLog.info(`Received From Channel: ${msg.text}`);
    if (!undefinedOrNull(msgToName)) {
      client.publish(`${mqttTopic}_msg`, JSON.stringify({
        "sendmsg": true,
        "to": msgToName,
        "message": msg
      }));
    } else {
      TelLog.error('Received message but can\'t send because no msgToName defined!');
    }
  }
});

