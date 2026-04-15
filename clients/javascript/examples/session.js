#!/usr/bin/env node
/**
 * Session-based conversation example
 *
 * This example shows how to use sessions to maintain conversation context.
 */

import { SigoClient, SigoError } from '../client.js';

async function main() {
  const client = new SigoClient('http://127.0.0.1:9080');

  const isAlive = await client.ping();
  if (!isAlive) {
    console.error('❌ sigoREST server is not responding');
    process.exit(1);
  }

  const sessionId = 'js-example-session';
  const model = 'kimi';

  console.log('💡 Session-based conversation example');
  console.log(`   Session: ${sessionId}`);
  console.log(`   Model: ${model}`);
  console.log();

  try {
    // First message
    console.log('👤 User: My name is Carol and I love JavaScript.');
    let response = await client.chat(
      model,
      'My name is Carol and I love JavaScript.',
      { sessionId }
    );
    console.log(`🤖 Assistant: ${response.content}\n`);

    // Second message - context is preserved via session
    console.log("👤 User: What's my name and what do I like?");
    response = await client.chat(
      model,
      "What's my name and what do I like?",
      { sessionId }
    );
    console.log(`🤖 Assistant: ${response.content}\n`);

    // Third message with system prompt
    console.log('👤 User: Can you recommend a JavaScript framework?');
    response = await client.chat(
      model,
      'Can you recommend a JavaScript framework?',
      {
        sessionId,
        systemPrompt: 'You are a helpful JavaScript expert. Be concise.'
      }
    );
    console.log(`🤖 Assistant: ${response.content}`);

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
