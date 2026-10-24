import http from 'k6/http';
import { check } from 'k6';

const base = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const smoke = __ENV.SMOKE === '1';

export const options = {
  scenarios: {
    postings: {
      executor: 'ramping-arrival-rate',
      startRate: smoke ? 5 : 10,
      timeUnit: '1s',
      preAllocatedVUs: smoke ? 20 : 50,
      maxVUs: smoke ? 40 : 400,
      stages: smoke
        ? [
            { target: 20, duration: '10s' },
            { target: 20, duration: '10s' },
          ]
        : [
            { target: 50, duration: '1m' },
            { target: 200, duration: '2m' },
            { target: 500, duration: '2m' },
          ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
  },
};

export function setup() {
  const stamp = Date.now().toString();
  const cash = http.post(
    `${base}/accounts`,
    JSON.stringify({ name: `cash-${stamp}`, currency: 'ZAR', type: 'asset' }),
    { headers: { 'Content-Type': 'application/json' } },
  );
  const alice = http.post(
    `${base}/accounts`,
    JSON.stringify({ name: `alice-${stamp}`, currency: 'ZAR', type: 'liability' }),
    { headers: { 'Content-Type': 'application/json' } },
  );
  return { cash: cash.json('id'), alice: alice.json('id') };
}

export default function (data) {
  const key = uniqueKey();
  const res = http.post(
    `${base}/transactions`,
    JSON.stringify({
      reference: `dep-${key}`,
      entries: [
        { account_id: data.cash, amount: 100 },
        { account_id: data.alice, amount: -100 },
      ],
    }),
    {
      headers: {
        'Content-Type': 'application/json',
        'Idempotency-Key': key,
      },
    },
  );
  check(res, { 'created': (r) => r.status === 201 });
}

function uniqueKey() {
  const bytes = new Uint8Array(16);
  for (let i = 0; i < bytes.length; i++) {
    bytes[i] = Math.floor(Math.random() * 256);
  }
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0'));
  return `${hex.slice(0, 4).join('')}-${hex.slice(4, 6).join('')}-${hex.slice(6, 8).join('')}-${hex.slice(8, 10).join('')}-${hex.slice(10).join('')}`;
}
