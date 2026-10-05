import type { Messenger } from "./port";
export class ConsoleMessenger implements Messenger {
  send(message: string): void { console.log(message); }
}
