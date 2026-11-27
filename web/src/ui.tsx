import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode, TextareaHTMLAttributes } from 'react'
import { Component } from 'react'
import * as RD from '@radix-ui/react-dialog'
import { useState } from 'react'

/* ---------- 错误边界：组件崩了显示错误卡而不是整页黑屏 ---------- */

interface BoundaryState {
  error: string | null
}

export class ErrorBoundary extends Component<{ children: ReactNode }, BoundaryState> {
  state: BoundaryState = { error: null }

  static getDerivedStateFromError(err: unknown): BoundaryState {
    return { error: err instanceof Error ? err.message : String(err) }
  }

  render() {
    if (this.state.error) {
      return (
        <div className="flex h-full flex-col items-center justify-center gap-3 p-8">
          <div className="mono-label text-danger">RENDER ERROR</div>
          <div className="max-w-[640px] overflow-auto border border-danger/40 bg-bg px-4 py-3 font-mono text-[11.5px] leading-5 text-muted">
            {this.state.error}
          </div>
          <Btn onClick={() => this.setState({ error: null })}>重试渲染</Btn>
        </div>
      )
    }
    return this.props.children
  }
}

export function cn(...parts: (string | false | null | undefined)[]) {
  return parts.filter(Boolean).join(' ')
}

/* ---------- 按钮 ---------- */

type BtnVariant = 'primary' | 'ghost' | 'danger' | 'icon'

export function Btn({
  variant = 'ghost',
  className,
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: BtnVariant }) {
  const base =
    'inline-flex h-8 select-none items-center justify-center gap-1.5 px-3 text-[12px] transition-colors disabled:cursor-not-allowed disabled:opacity-40'
  const styles: Record<BtnVariant, string> = {
    primary: 'bg-accent font-medium text-bg hover:brightness-110 active:brightness-95',
    ghost: 'border border-line text-text hover:border-line2 hover:bg-surface2',
    danger: 'border border-danger/40 text-danger hover:bg-danger/10 hover:border-danger/70',
    icon: 'h-8 w-8 px-0 text-muted hover:bg-surface2 hover:text-text',
  }
  return (
    <button className={cn(base, styles[variant], className)} {...rest}>
      {children}
    </button>
  )
}

/* ---------- 表单 ---------- */

export function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: ReactNode
  children: ReactNode
}) {
  return (
    <label className="block">
      <div className="mono-label mb-1.5">{label}</div>
      {children}
      {hint && <div className="mt-1.5 text-[11px] leading-5 text-muted">{hint}</div>}
    </label>
  )
}

export function TextInput({
  mono,
  className,
  ...rest
}: InputHTMLAttributes<HTMLInputElement> & { mono?: boolean }) {
  return (
    <input
      className={cn(
        'h-9 w-full border border-line bg-bg px-3 text-[13px] text-text placeholder:text-muted/60 focus:border-accent/70 focus:outline-none',
        mono && 'font-mono',
        className,
      )}
      {...rest}
    />
  )
}

export function TextArea({
  mono,
  className,
  ...rest
}: TextareaHTMLAttributes<HTMLTextAreaElement> & { mono?: boolean }) {
  return (
    <textarea
      className={cn(
        'w-full resize-y border border-line bg-bg p-3 text-[13px] leading-6 text-text placeholder:text-muted/60 focus:border-accent/70 focus:outline-none',
        mono && 'font-mono',
        className,
      )}
      {...rest}
    />
  )
}

/* ---------- 对话框 ---------- */

const overlay = 'fixed inset-0 z-40 bg-black/60 overlay-in'

export function Drawer({
  open,
  onOpenChange,
  title,
  children,
  width = 480,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  title: string
  children: ReactNode
  width?: number
}) {
  return (
    <RD.Root open={open} onOpenChange={onOpenChange}>
      <RD.Portal>
        <RD.Overlay className={overlay} />
        <RD.Content
          className="drawer-content fixed inset-y-0 right-0 z-50 flex flex-col border-l border-line bg-surface"
          style={{ width }}
        >
          <div className="flex items-center justify-between border-b border-line px-5 py-4">
            <div className="mono-label">{title}</div>
            <RD.Close asChild>
              <Btn variant="icon" aria-label="关闭">
                ✕
              </Btn>
            </RD.Close>
          </div>
          <div className="flex-1 overflow-y-auto p-5">{children}</div>
        </RD.Content>
      </RD.Portal>
    </RD.Root>
  )
}

export function Modal({
  open,
  onOpenChange,
  title,
  children,
  width = 420,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  title: string
  children: ReactNode
  width?: number
}) {
  return (
    <RD.Root open={open} onOpenChange={onOpenChange}>
      <RD.Portal>
        <RD.Overlay className={overlay} />
        <RD.Content
          className="modal-content fixed left-1/2 top-1/3 z-50 -translate-x-1/2 border border-line bg-surface p-5"
          style={{ width }}
        >
          <div className="mono-label mb-3">{title}</div>
          {children}
        </RD.Content>
      </RD.Portal>
    </RD.Root>
  )
}

export function Confirm({
  open,
  onOpenChange,
  onConfirm,
  title,
  children,
  confirmText = '删除',
  busy,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onConfirm: () => Promise<void> | void
  title: string
  children: ReactNode
  confirmText?: string
  busy?: boolean
}) {
  return (
    <Modal open={open} onOpenChange={onOpenChange} title={title}>
      <div className="text-[13px] leading-6 text-text">{children}</div>
      <div className="mt-5 flex justify-end gap-2">
        <Btn onClick={() => onOpenChange(false)}>取消</Btn>
        <Btn variant="danger" disabled={busy} onClick={() => void onConfirm()}>
          {busy ? '删除中…' : confirmText}
        </Btn>
      </div>
    </Modal>
  )
}

/* ---------- 其他 ---------- */

export function ErrorText({ children }: { children?: ReactNode }) {
  if (!children) return null
  return <div className="mt-2 border-l-2 border-danger/60 pl-3 text-[12px] text-danger">{children}</div>
}

export function EmptyState({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 py-16">
      <div className="mono-label text-[11px]">{title}</div>
      {hint && <div className="text-[12px] text-muted">{hint}</div>}
      {action}
    </div>
  )
}

export function CopyBtn({ getText, className }: { getText: () => Promise<string> | string; className?: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      className={cn('h-8 w-8 px-0 text-muted transition-colors hover:bg-surface2 hover:text-text', className)}
      title="复制"
      onClick={async () => {
        try {
          const text = await getText()
          await navigator.clipboard.writeText(text)
          setCopied(true)
          setTimeout(() => setCopied(false), 900)
        } catch {
          /* 剪贴板不可用时静默 */
        }
      }}
    >
      {copied ? <span className="font-mono text-accent">✓</span> : '⧉'}
    </button>
  )
}
