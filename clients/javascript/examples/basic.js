#!/usr/bin/env node
/**
 * Basic chat example with sigoclient
 *
 * This example shows how to send a simple chat message to sigoREST.
 */

import { SigoClient, SigoError } from '../client.js';

async function main() {
  // Create client (connects to 127.0.0.1:9080 by default)
  const client = new SigoClient('http://127.0.0.1:9080');

  // Check if server is alive
  const isAlive = await client.ping();
  if (!isAlive) {
    console.error('❌ sigoREST server is not responding');
    console.error('   Make sure the server is running: sudo systemctl start sigoREST');
    process.exit(1);
  }

  console.log('✅ Connected to sigoREST');
  console.log();

  // Simple chat
  try {
    const response = await client.chat(
      'kimi',
      'Explain quantum computing in one sentence.'
    );
    console.log(`🤖 Model: ${response.model}`);
    console.log(`💬 Response: ${response.content}`);
  } catch (error) {
    if (error instanceof SigoError) {
      console.error(`❌ Error: ${error.message}`);
    } else {
      console.error(`❌ Unexpected error: ${error}`);
    }
    process.exit(1);
  }
}

main();
