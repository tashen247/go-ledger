import http from 'k6/http';
import { check } from 'k6';

const base = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const smoke = __ENV.SMOKE === '1';
const vus = smoke ? 20 : 200;

http.setResponseCallback(http.expectedStatuses(201, 409));

export const options = {
  scenarios: {
    storm: {
      executor: 'shared-iterations',
      vus: vus,
      iterations: vus,
      maxDuration: '2m',
    },
  },
  thresholds: {
    checks: ['rate>0.99'],
  },
};

export function setup() {
  const stamp = Date.now().toString();
  const cash = http.post(
    `${base}/accounts`,
    JSON.stringify({ name: `storm-cash-${stamp}`, currency: 'ZAR', type: 'asset' }),
    { headers: { 'Content-Type': 'application/json' } },
  );
  const alice = http.post(
    `${base}/accounts`,
    JSON.stringify({ name: `storm-alice-${stamp}`, currency: 'ZAR', type: 'liability' }),
    { headers: { 'Content-Type': 'application/json' } },
  );
  return {
    cash: cash.json('id'),
    alice: alice.json('id'),
    key: `storm-${stamp}`,
  };
}

export default function (data) {
  const res = http.post(
    `${base}/transactions`,
    JSON.stringify({
      reference: 'storm',
      entries: [
        { account_id: data.cash, amount: 100 },
        { account_id: data.alice, amount: -100 },
      ],
    }),
    {
      headers: {
        'Content-Type': 'application/json',
        'Idempotency-Key': data.key,
      },
    },
  );
  check(res, {
    'created or in flight': (r) => r.status === 201 || r.status === 409,
  });
}
