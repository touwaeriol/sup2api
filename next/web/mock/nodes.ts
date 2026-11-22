// Mock cluster nodes (GET /nodes): one serving, one switching plugin
// versions, one gone quiet with a failed plugin.
import { now, on } from './router'

const bootA = 'b7f1c2d4-8e9a-4b0c-a1d2-3e4f5a6b7c8d'
const bootB = 'c0ffee00-1234-4abc-9def-0123456789ab'
const startedA = now(-86400 * 2 - 3600 * 5)
const startedB = now(-3600 * 7)

on('GET', '/nodes', () => [
  {
    node_id: 'node-1',
    boot_id: bootA,
    addr: '10.0.0.11:8080',
    host_version: '0.1.0',
    started_at: startedA,
    last_heartbeat: now(-Math.floor(Math.random() * 4)),
    plugins: {
      anthropic: JSON.stringify({ serving: '0.2.1', state: 'active', instances: [{ version: '0.2.1', state: 'ready', restarts: 0 }] }),
      guard: { serving: '0.1.0', state: 'active', instances: [{ version: '0.1.0', state: 'ready', restarts: 0 }] }
    }
  },
  {
    node_id: 'node-2',
    boot_id: bootB,
    addr: '10.0.0.12:8080',
    host_version: '0.1.0',
    started_at: startedB,
    last_heartbeat: now(-Math.floor(Math.random() * 4)),
    plugins: {
      anthropic: JSON.stringify({ serving: '0.2.0', standby: '0.2.1', fallback: '0.2.0', state: 'pending', instances: [{ version: '0.2.0', state: 'ready', restarts: 1 }] }),
      guard: JSON.stringify({ serving: '0.1.0', standby: '0.2.0', state: 'pending' })
    }
  },
  {
    node_id: 'node-3',
    boot_id: 'dead0000-0000-4000-8000-000000000003',
    addr: '10.0.0.13:8080',
    host_version: '0.0.9',
    started_at: now(-86400 * 9),
    last_heartbeat: now(-95),
    plugins: {
      anthropic: JSON.stringify({ serving: '0.2.0', fallback: '0.2.0', state: 'failed', error: 'health check timeout' })
    }
  }
])
