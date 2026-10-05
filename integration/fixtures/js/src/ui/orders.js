import { storeOrder } from '../domain/store.js';

export function submitOrder(order) {
  return storeOrder(order);
}
