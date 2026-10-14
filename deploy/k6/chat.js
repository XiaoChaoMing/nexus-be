// k6 load test for the realtime gateway (GĐ3 WP6): many viewers in one chat room, a few senders.
//
//   k6 run -e BASE=http://localhost:8080 -e STREAM_ID=<live stream id> deploy/k6/chat.js
//
// Viewers connect as guests (read-only) and subscribe to chat:{id} and stream:{id}; senders register
// throw-away accounts in setup() and post a message every SEND_EVERY_MS. Every message body carries its send
// time, so receivers record send→receive latency in the `chat_delivery_ms` trend.
// The stream must be LIVE (chat room OPEN) with every chat mode off. All senders share one IP, so raise
// CHAT_RATE_LIMIT_IP on chat-svc for the run (SENDERS × messages per CHAT_RATE_WINDOW).
import http from 'k6/http'
import ws from 'k6/ws'
import { check } from 'k6'
import exec from 'k6/execution'
import { Counter, Trend } from 'k6/metrics'

const BASE = __ENV.BASE || 'http://localhost:8080'
const WS_URL = BASE.replace(/^http/, 'ws') + '/v1/ws'
const ORIGIN = __ENV.ORIGIN || 'http://localhost:5173'
const STREAM_ID = __ENV.STREAM_ID
const VIEWERS = Number(__ENV.VIEWERS || 1000)
const SENDERS = Number(__ENV.SENDERS || 20)
const RAMP = __ENV.RAMP || '60s'
const HOLD = __ENV.HOLD || '120s'
const SEND_EVERY_MS = Number(__ENV.SEND_EVERY_MS || 2000)

const delivery = new Trend('chat_delivery_ms', true)
const received = new Counter('chat_messages_received')
const sent = new Counter('chat_messages_sent')
const refused = new Counter('chat_send_refused')
const connectFailed = new Counter('ws_connect_failed')

const holdSeconds = parseInt(HOLD, 10)
const rampSeconds = parseInt(RAMP, 10)

export const options = {
  scenarios: {
    viewers: {
      executor: 'ramping-vus',
      exec: 'viewer',
      startVUs: 0,
      stages: [
        { duration: RAMP, target: VIEWERS },
        { duration: HOLD, target: VIEWERS },
        { duration: '10s', target: 0 },
      ],
      gracefulRampDown: '15s',
    },
    senders: {
      executor: 'per-vu-iterations',
      exec: 'sender',
      vus: SENDERS,
      iterations: 1,
      startTime: RAMP,
      maxDuration: `${holdSeconds + 30}s`,
    },
  },
  thresholds: {
    chat_delivery_ms: ['p(95)<500'],
    ws_connect_failed: ['count<10'],
  },
}

const headers = { 'Content-Type': 'application/json', 'X-NXL-Client': 'web', Origin: ORIGIN }

export function setup() {
  if (!STREAM_ID) throw new Error('STREAM_ID is required (a LIVE stream)')
  const tokens = []
  const run = Date.now().toString(36)
  for (let i = 0; i < SENDERS; i++) {
    const username = `k6${run}${i}`.slice(0, 25)
    const body = JSON.stringify({ username, displayName: username, email: `${username}@k6.local`, password: 'k6-password-1', acceptTerms: true })
    const res = http.post(`${BASE}/v1/auth/register`, body, { headers })
    check(res, { registered: (r) => r.status === 201 })
    tokens.push(res.json('data.accessToken'))
  }

  return { tokens }
}

function uuid() {
  const h = '0123456789abcdef'
  let s = ''
  for (let i = 0; i < 32; i++) s += h[Math.floor(Math.random() * 16)]

  return `${s.slice(0, 8)}-${s.slice(8, 12)}-4${s.slice(13, 16)}-a${s.slice(17, 20)}-${s.slice(20, 32)}`
}

function onMessage(raw) {
  const f = JSON.parse(raw)
  if (f.op !== 'event' || f.type !== 'message') return
  const m = /^k6 (\d+) /.exec(f.data.body || '')
  if (!m) return
  received.add(1)
  delivery.add(Date.now() - Number(m[1]))
}

export function viewer() {
  const res = ws.connect(WS_URL, { headers: { Origin: ORIGIN } }, (socket) => {
    socket.on('open', () => {
      socket.send(JSON.stringify({ op: 'auth', token: null, anonId: uuid() }))
    })
    socket.on('message', (raw) => {
      const f = JSON.parse(raw)
      if (f.op === 'ready') {
        socket.send(JSON.stringify({ op: 'subscribe', topic: `chat:${STREAM_ID}` }))
        socket.send(JSON.stringify({ op: 'subscribe', topic: `stream:${STREAM_ID}` }))

        return
      }
      onMessage(raw)
    })
    socket.setInterval(() => socket.send(JSON.stringify({ op: 'ping' })), 25000)
    socket.setTimeout(() => socket.close(), (rampSeconds + holdSeconds) * 1000)
  })
  if (!res || res.status !== 101) connectFailed.add(1)
}

export function sender(data) {
  // One iteration per sender VU: the scenario-wide iteration number picks a distinct account.
  const token = data.tokens[exec.scenario.iterationInTest % data.tokens.length]
  const res = ws.connect(WS_URL, { headers: { Origin: ORIGIN } }, (socket) => {
    socket.on('open', () => socket.send(JSON.stringify({ op: 'auth', token })))
    socket.on('message', (raw) => {
      const f = JSON.parse(raw)
      if (f.op === 'ready') {
        socket.send(JSON.stringify({ op: 'subscribe', topic: `chat:${STREAM_ID}` }))
        socket.setInterval(() => {
          const body = `k6 ${Date.now()} vu${__VU}`
          socket.send(JSON.stringify({ op: 'send', topic: `chat:${STREAM_ID}`, type: 'message', data: { body }, clientMsgId: uuid() }))
          sent.add(1)
        }, SEND_EVERY_MS)

        return
      }
      if (f.op === 'error') {
        refused.add(1, { code: f.code })
        if (__ENV.DEBUG) console.log(`vu${__VU} ${raw}`)
      }
    })
    socket.setInterval(() => socket.send(JSON.stringify({ op: 'ping' })), 25000)
    socket.setTimeout(() => socket.close(), holdSeconds * 1000)
  })
  if (!res || res.status !== 101) connectFailed.add(1)
}
