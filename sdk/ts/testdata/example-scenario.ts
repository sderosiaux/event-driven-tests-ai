// Example input for `edt-ts compile`. Used by the CLI smoke test.
import { scenario } from "../src/index.js";

export default scenario("example-from-ts")
  .kafka("localhost:9092")
  .produce("p", {
    topic: "orders",
    payload: "${data.orders}",
    count: 10,
  })
  .check("seen_one", "size(stream('orders')) >= 1", { severity: "critical" })
  .build();
