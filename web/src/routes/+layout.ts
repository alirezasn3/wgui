// The panel is entirely client-driven: it needs a live connection to the API for
// every view, so there is nothing to prerender or render on a server.
export const prerender = false;
export const ssr = false;
export const trailingSlash = 'never';
