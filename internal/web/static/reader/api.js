// JSON API client for the reader. The CSRF token comes from the page meta
// tag and is sent as X-CSRF-Token; cookies stay HttpOnly.

const meta = document.querySelector('meta[name="csrf-token"]');

export const csrfToken = meta ? meta.content : "";

export async function api(path, { method = "GET", body, keepalive = false } = {}) {
  const headers = { Accept: "application/json" };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  if (method !== "GET" && method !== "HEAD") {
    headers["X-CSRF-Token"] = csrfToken;
  }

  const response = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
    keepalive,
  });

  if (response.status === 204) {
    return null;
  }

  let data = null;
  try {
    data = await response.json();
  } catch {
    data = null;
  }
  if (!response.ok) {
    const error = new Error(
      data && data.error ? data.error : `Request failed (${response.status})`,
    );
    error.status = response.status;
    throw error;
  }
  return data;
}
