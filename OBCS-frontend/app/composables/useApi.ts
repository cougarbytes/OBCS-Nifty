// useApi wraps $fetch to the Go backend, attaching the Supabase access token as
// a bearer credential so the backend's auth middleware accepts the request.
export function useApi() {
  const config = useRuntimeConfig()
  const supabase = useSupabaseClient()
  const base = config.public.apiBase as string

  async function request<T>(path: string, opts: Record<string, unknown> = {}): Promise<T> {
    const { data } = await supabase.auth.getSession()
    const token = data.session?.access_token
    const headers: Record<string, string> = {
      ...(opts.headers as Record<string, string> | undefined),
    }
    if (token) headers.Authorization = `Bearer ${token}`
    return await $fetch<T>(base + path, { ...opts, headers })
  }

  return {
    get: <T>(path: string) => request<T>(path),
    post: <T>(path: string, body?: unknown) =>
      request<T>(path, { method: 'POST', body }),
  }
}
