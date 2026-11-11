// Read-only load test for altserv.
//
// Install k6, then:
//   BASE_URL=https://altserv.example.invalid \
//   LOAD_EMAIL=loadtest@example.invalid \
//   LOAD_PASSWORD='...' \
//   k6 run deployments/loadtest/k6.js
//
// The test logs in once in setup and shares the session cookie with the VUs,
// so the login rate limiter is not part of the measurement. The account must
// already exist. Create a dedicated read-only employee account for load tests;
// never use a real chef account.
//
// The default scenarios only read. They do not create patients, reports or
// billing runs, so a run does not pollute production data.

import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const LOAD_EMAIL = __ENV.LOAD_EMAIL || '';
const LOAD_PASSWORD = __ENV.LOAD_PASSWORD || '';

export const options = {
  scenarios: {
    read_paths: {
      executor: 'constant-vus',
      vus: Number(__ENV.VUS || 10),
      duration: __ENV.DURATION || '2m',
      gracefulStop: '30s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<500', 'p(99)<1500'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  if (!LOAD_EMAIL || !LOAD_PASSWORD) {
    throw new Error('LOAD_EMAIL and LOAD_PASSWORD are required');
  }

  const response = http.post(
    `${BASE_URL}/api/login`,
    JSON.stringify({ email: LOAD_EMAIL, password: LOAD_PASSWORD }),
    { headers: { 'Content-Type': 'application/json' }, tags: { name: 'setup_login' } },
  );
  check(response, { 'setup login succeeded': (r) => r.status === 200 });

  const cookie = response.cookies['__Host-session'];
  if (!cookie || !cookie.length) {
    throw new Error('login did not return the __Host-session cookie');
  }
  const csrf = response.json('csrf_token');
  if (!csrf) {
    throw new Error('login did not return a CSRF token');
  }
  return { cookie: cookie[0].value, csrf };
}

export default function (data) {
  const headers = {
    Cookie: `__Host-session=${data.cookie}`,
    'X-CSRF-Token': data.csrf,
  };

  const me = http.get(`${BASE_URL}/api/me`, { headers, tags: { name: 'GET /api/me' } });
  check(me, { 'me is 200': (r) => r.status === 200 });

  const patients = http.get(`${BASE_URL}/api/patients`, { headers, tags: { name: 'GET /api/patients' } });
  check(patients, { 'patient list is 200': (r) => r.status === 200 });

  const health = http.get(`${BASE_URL}/healthz`, { tags: { name: 'GET /healthz' } });
  check(health, { 'liveness is 200': (r) => r.status === 200 });

  const ready = http.get(`${BASE_URL}/readyz`, { tags: { name: 'GET /readyz' } });
  check(ready, { 'readiness is 200': (r) => r.status === 200 });

  sleep(Number(__ENV.SLEEP || 0.5));
}
