#!/usr/bin/env node
/**
 * List available models example
 *
 * This example shows how to get information about available models.
 */

import { SigoClient } from '../client.js';

function getProvider(model) {
  if (model.endpoint.includes('mammouth')) return 'Mammoth.ai';
  if (model.endpoint.includes('moonshot')) return 'Moonshot';
  if (model.endpoint.includes('z.ai')) return 'Z.ai';
  if (model.shortcode.startsWith('ollama-')) return 'Ollama (Local)';
  return 'Other';
}

async function main() {
  const client = new SigoClient('http://127.0.0.1:9080');

  const isAlive = await client.ping();
  if (!isAlive) {
    console.error('❌ sigoREST server is not responding');
    process.exit(1);
  }

  console.log('📋 Available Models\n');

  // Get health info
  const health = await client.health();
  console.log(`Server Status: ${health.status}`);
  console.log(`Available Models: ${health.available_models}`);
  console.log(`Memory Set: ${health.memory_set}`);
  console.log();

  // List all models
  const models = await client.listModels();

  // Group by provider
  const providers = {};
  for (const model of models) {
    const provider = getProvider(model);
    if (!providers[provider]) {
      providers[provider] = [];
    }
    providers[provider].push(model);
  }

  // Sort providers and display
  const sortedProviders = Object.keys(providers).sort();
  for (const provider of sortedProviders) {
    console.log(`\n--- ${provider} ---`);
    console.log(`${'ID'.padEnd(30)} ${'Shortcode'.padEnd(15)} ${'Input'.padStart(8)} ${'Output'.padStart(8)}`);
    console.log('-'.repeat(65));

    // Sort models by shortcode
    const sortedModels = providers[provider].sort((a, b) =>
      a.shortcode.localeCompare(b.shortcode)
    );

    for (const m of sortedModels) {
      console.log(
        `${m.id.padEnd(30)} ${m.shortcode.padEnd(15)} ` +
        `$${m.input_cost.toFixed(2).padStart(6)} $${m.output_cost.toFixed(2).padStart(6)}`
      );
    }
  }
}

main().catch(err => {
  console.error('❌ Error:', err.message);
  process.exit(1);
});
