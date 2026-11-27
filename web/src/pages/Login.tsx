import { useState } from 'react'
import { api, ApiError } from '../api'
import { Btn, ErrorText, Field, TextInput } from '../ui'

export function Login({ onSuccess }: { onSuccess: () => void }) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    if (busy || !password) return
    setBusy(true)
    setError('')
    try {
      await api.login(password)
      onSuccess()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '连接失败，检查服务是否在运行')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex h-full items-center justify-center">
      <div className="w-[360px]">
        <div className="mb-8 flex items-center gap-3">
          <span className="inline-block h-5 w-5 bg-accent" />
          <div>
            <div className="font-mono text-[14px] font-medium uppercase tracking-[0.22em] text-text">
              Secret Gate
            </div>
            <div className="mono-label mt-0.5">CI Secrets · OIDC Access</div>
          </div>
        </div>

        <div className="border border-line bg-surface p-6">
          <Field label="管理口令" hint="来自环境变量 ADMIN_PASSWORD">
            <TextInput
              type="password"
              mono
              autoFocus
              value={password}
              placeholder="········"
              onChange={(e) => setPassword(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void submit()
              }}
            />
          </Field>
          <ErrorText>{error}</ErrorText>
          <Btn
            variant="primary"
            className="mt-5 w-full"
            disabled={busy || !password}
            onClick={() => void submit()}
          >
            {busy ? '验证中…' : '进入后台'}
          </Btn>
        </div>

        <div className="mono-label mt-4 text-center">自托管 · 密文入库 · 零长期凭据</div>
      </div>
    </div>
  )
}
