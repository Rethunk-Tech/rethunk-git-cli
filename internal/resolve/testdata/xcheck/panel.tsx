import type { ReactNode } from "react"

export function Panel({ children }: { children: ReactNode }) {
  return <section className="panel">{children}</section>
}

export const Title = ({ text }: { text: string }) => <h2>{text}</h2>
