/**
 * sigoclient - JavaScript client for sigoREST API
 *
 * A simple, lightweight client for the sigoREST OpenAI-compatible API.
 * Works in Node.js (18+) and modern browsers.
 */

/**
 * Base error class for sigoREST client errors
 */
export class SigoError extends Error {
  constructor(message) {
    super(message);
    this.name = 'SigoError';
  }
}

/**
 * API error with response details
 */
export class SigoAPIError extends SigoError {
  constructor(message, statusCode = null, response = null) {
    super(message);
    this.name = 'SigoAPIError';
    this.statusCode = statusCode;
    this.response = response;
  }
}

/**
 * Client for sigoREST API
 *
 * @example
 * const client = new SigoClient('http://127.0.0.1:9080');
 * const response = await client.chat('kimi', 'Hello!');
 * console.log(response.content);
 */
export class SigoClient {
  /**
   * @param {string} baseUrl - URL of the sigoREST server
   * @param {object} options - Client options
   * @param {number} options.timeout - Default timeout in milliseconds (default: 180000)
   */
  constructor(baseUrl = 'http://127.0.0.1:9080', options = {}) {
    this.baseUrl = baseUrl.replace(/\/$/, '');
    this.timeout = options.timeout || 180000;
  }

  /**
   * Make an HTTP request to the API
   * @private
   */
  async _request(method, path, body = null, timeout = null) {
    const url = `${this.baseUrl}${path}`;
    const controller = new AbortController();
    const timeoutMs = timeout || this.timeout;

    const timeoutId = setTimeout(() => controller.abort(), timeoutMs);

    try {
      const options = {
        method,
        headers: {},
        signal: controller.signal,
      };

      if (body) {
        options.headers['Content-Type'] = 'application/json';
        options.body = JSON.stringify(body);
      }

      const response = await fetch(url, options);
      clearTimeout(timeoutId);

      if (!response.ok) {
        let errorMessage = `HTTP ${response.status}: ${response.statusText}`;
        let errorData = null;

        try {
          errorData = await response.json();
          if (errorData.error && errorData.error.message) {
            errorMessage = errorData.error.message;
          }
        } catch {
          // Ignore JSON parse error
        }

        throw new SigoAPIError(errorMessage, response.status, errorData);
      }

      // Handle empty responses (like ping)
      const contentType = response.headers.get('content-type');
      if (!contentType || !contentType.includes('application/json')) {
        const text = await response.text();
        return text;
      }

      return await response.json();
    } catch (error) {
      clearTimeout(timeoutId);

      if (error instanceof SigoAPIError) {
        throw error;
      }

      if (error.name === 'AbortError') {
        throw new SigoError(`Request to ${url} timed out after ${timeoutMs}ms`);
      }

      throw new SigoError(`Request failed: ${error.message}`);
    }
  }

  /**
   * Check if server is alive
   * @returns {Promise<boolean>}
   */
  async ping() {
    try {
      const result = await this._request('GET', '/ping', null, 5000);
      return result === 'pong';
    } catch {
      return false;
    }
  }

  /**
   * Get server health status
   * @returns {Promise<object>} Health status with available_models, circuit_breakers, etc.
   */
  async health() {
    return await this._request('GET', '/api/health');
  }

  /**
   * List all available models
   * @returns {Promise<Array<ModelInfo>>}
   */
  async listModels() {
    return await this._request('GET', '/api/models');
  }

  /**
   * Send a chat completion request
   *
   * @param {string} model - Model shortcode (e.g., "kimi", "gpt41") or full ID
   * @param {string} message - The user message
   * @param {object} options - Request options
   * @param {string} options.sessionId - Session ID for conversation continuity
   * @param {string} options.systemPrompt - System prompt/context
   * @param {number} options.temperature - Temperature (0.0-2.0)
   * @param {number} options.maxTokens - Max tokens to generate
   * @param {number} options.timeout - Request timeout in milliseconds
   * @param {number} options.retries - Number of retries (default: 3)
   * @returns {Promise<ChatResponse>}
   */
  async chat(model, message, options = {}) {
    const messages = [];

    if (options.systemPrompt) {
      messages.push({ role: 'system', content: options.systemPrompt });
    }

    messages.push({ role: 'user', content: message });

    const payload = {
      model,
      messages,
      retries: options.retries ?? 3,
    };

    if (options.temperature !== undefined) {
      payload.temperature = options.temperature;
    }

    if (options.maxTokens !== undefined) {
      payload.max_tokens = options.maxTokens;
    }

    if (options.sessionId !== undefined) {
      payload.session_id = options.sessionId;
    }

    if (options.timeout !== undefined) {
      payload.timeout = Math.floor(options.timeout / 1000); // Convert to seconds
    }

    const data = await this._request(
      'POST',
      '/v1/chat/completions',
      payload,
      options.timeout
    );

    if (!data.choices || data.choices.length === 0) {
      throw new SigoError('No choices in response');
    }

    return {
      content: data.choices[0].message.content,
      model: data.model,
      sessionId: options.sessionId,
      rawResponse: data,
    };
  }

  /**
   * Get the global memory block
   * @returns {Promise<MemoryBlock>}
   */
  async getMemory() {
    return await this._request('GET', '/api/memory');
  }

  /**
   * Set the global memory block
   * @param {string} content - The system context/prompt
   * @param {boolean} cache - Whether to use prompt caching
   * @returns {Promise<MemoryBlock>}
   */
  async setMemory(content, cache = true) {
    return await this._request('PUT', '/api/memory', { content, cache });
  }
}

/**
 * @typedef {object} ModelInfo
 * @property {string} id - Full model ID
 * @property {string} shortcode - Shortcode for the model
 * @property {string} endpoint - API endpoint
 * @property {string} apikey - Environment variable for API key
 * @property {number} max_input_tokens - Maximum input tokens
 * @property {number} max_output_tokens - Maximum output tokens
 * @property {number} input_cost - Input cost per 1M tokens
 * @property {number} output_cost - Output cost per 1M tokens
 * @property {number} min_temperature - Minimum temperature
 * @property {number} max_temperature - Maximum temperature
 * @property {boolean} requires_completion_tokens - Whether model requires completion tokens
 */

/**
 * @typedef {object} ChatResponse
 * @property {string} content - The assistant's response
 * @property {string} model - The model used
 * @property {string} [sessionId] - Session ID if used
 * @property {object} rawResponse - Raw API response
 */

/**
 * @typedef {object} MemoryBlock
 * @property {string} content - System context/prompt
 * @property {boolean} cache - Whether caching is enabled
 */

export default SigoClient;
