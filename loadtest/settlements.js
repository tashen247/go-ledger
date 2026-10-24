import http from 'k6/http';
import { check } from 'k6';

const base = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const smoke = __ENV.SMOKE === '1';
const events = smoke ? 10 : 100;

export const options = {
  scenarios: {
    duplicates: {
      executor: 'shared-iterations',
      vus: smoke ? 5 : 20,
      iterations: events * 3,
      maxDuration: '2m',
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
    JSON.stringify({ name: `set-cash-${stamp}`, currency: 'ZAR', type: 'asset' }),
    { headers: { 'Content-Type': 'application/json' } },
  );
  const alice = http.post(
    `${base}/accounts`,
    JSON.stringify({ name: `set-alice-${stamp}`, currency: 'ZAR', type: 'liability' }),
    { headers: { 'Content-Type': 'application/json' } },
  );
  const cashID = cash.json('id');
  const aliceID = alice.json('id');
  const txn = http.post(
    `${base}/transactions`,
    JSON.stringify({
      reference: `set-${stamp}`,
      entries: [
        { account_id: cashID, amount: 100 },
        { account_id: aliceID, amount: -100 },
      ],
    }),
    {
      headers: {
        'Content-Type': 'application/json',
        'Idempotency-Key': `set-txn-${stamp}`,
      },
    },
  );
  const jobs = [];
  for (let i = 0; i < events; i++) {
    const body = JSON.stringify({
      external_id: `psp-${stamp}-${i}`,
      transaction_id: txn.json('id'),
      status: 'settled',
      amount: 100,
      currency: 'ZAR',
    });
    jobs.push(body, body, body);
  }
  for (let i = jobs.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    const tmp = jobs[i];
    jobs[i] = jobs[j];
    jobs[j] = tmp;
  }
  return { jobs };
}

export default function (data) {
  const body = data.jobs[__ITER % data.jobs.length];
  const res = http.post(`${base}/settlements`, body, {
    headers: { 'Content-Type': 'application/json' },
  });
  check(res, { 'accepted': (r) => r.status === 200 });
}
