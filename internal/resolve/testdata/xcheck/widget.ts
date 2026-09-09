import { readFile } from "node:fs/promises"

export interface Options {
  path: string
}

export async function load(opts: Options): Promise<string> {
  return readFile(opts.path, "utf8")
}

export class Widget {
  constructor(private readonly name: string) {}

  render(): string {
    return this.name
  }
}
