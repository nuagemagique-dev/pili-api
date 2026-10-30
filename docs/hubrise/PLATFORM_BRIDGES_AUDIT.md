# Platform bridges audit (Just Eat, Deliveroo, Uber Eats)

Every page of the three HubRise bridge documentations, set against the code, on 2026-10-30. Platform orders are accepted in pili-pos only. Everything was checked against mocks: no real platform order has gone through yet.

Verdicts: **PASS** the code already did it. **FIXED** it did not, it does now, with a test. **CONFIG** no code, a setting or a person. **UNKNOWN** needs a real order or a question to HubRise.

## Orders

| Rule from the docs | Platforms | Where | Verdict |
|---|---|---|---|
| Orders come with a `channel`, its text is not documented; an unknown channel must not block service | All | `webshop.rs` `channel_key` | FIXED: loose match on channel, then connection and creator. Real texts UNKNOWN |
| `product_name` and option `name` are the ref code when a ref exists | Deliveroo | `webshop.rs` `product_display_name`, `option_display_name`; board, tickets, fiscal text | FIXED |
| `expected_time` carries the time asked for; `confirmed_time` overrides it, and on Deliveroo the gap is sent as a delay | Uber, Deliveroo | `hubrise.rs` `summary_from_raw`, `print.rs` | FIXED: accepting no longer moves the time or sends a false 10 minute delay |
| Answer within 3 minutes (Just Eat, Deliveroo), 10 minutes (Uber) | All | Both UIs `isLate` | FIXED: alarm at 2 minutes on a platform, it was 5 |
| Status mapping: accepted, rejected, in_preparation, awaiting_collection, completed, cancelled | All | `commands/webshop.rs` | PASS |
| Accept on `accepted`, not `received` (the POS sends `received` by itself) | All | Bridge setting | CONFIG |
| A later time reaches the customer only at acceptance (Uber), before in_preparation (Deliveroo), never (Just Eat) | All | Both UIs `canDelay` | FIXED: the delay is hidden when nobody would hear of it |
| Door notes in `customer.delivery_notes`, access code | Deliveroo, Uber | Board, customer slip | FIXED |
| `address_2` on the slip | All | `customer_slip.rs` | FIXED |
| `driver_pickup_url` for the restaurant's own driver, shown as a QR | Uber | `customer_slip.rs` | FIXED. Uber's alternative, Delivery status updates, is not built |
| Charges: delivery, service, small order, bag, tip, platform fee | All | `webshop.rs` `resolve` | FIXED: on an order with an address, a charge the catalog knows is the restaurant's and is signed; any other stays out of the sale. Accountant to confirm |
| Unpaid orders: the card amount must match the signed sale | Own delivery | `net_total_cents`, both stores | FIXED: it was refused, and on a linked terminal only after charging the card |
| Payments empty or absent on an online order | All | `classify_payment` | UNKNOWN: read as unpaid. Sample orders needed |
| Customer email placeholder, support phone, blank names | Just Eat | Not read as real data | PASS |
| VAT: no item tax rate from the bridges | All | Menu VAT | PASS |
| Marked up prices differ from the catalog | All | `PriceDiffers` is only a warning | PASS |
| Discounts: one object (Deliveroo), array (Uber), none (Just Eat) | All | `spread_discount` | PASS |
| Options without a list name | All | `OrderOption` default | FIXED |
| Cancellation before and after signing | All | `follow_cancellation`, refund | PASS |
| An order that timed out on the platform stays open in HubRise | Uber, Just Eat | Accepting it would sign an N | UNKNOWN: what HubRise shows then |
| Pause and preparation time sync | Deliveroo, Uber | Not built | CONFIG: optional, not for Just Eat in Belgium |

## Catalog

| Rule from the docs | Platforms | Where | Verdict |
|---|---|---|---|
| Every product and option needs a ref; a missing ref fails the order (Deliveroo) or skips the product (Just Eat) | All | `catalog_publish_refs.go` | PASS: assigned on publish |
| Prices per platform through variants and `price_overrides` | All | `writePlatformPrices`, dashboard form | FIXED. Percentages still to come from the restaurant |
| Hidden products: bridges ignore tags | All | `mergeHiddenRestriction` | FIXED with price lists. Whether every bridge honours `variant_refs` is UNKNOWN |
| Category description reaches customers | All | `mergeCategoryNames` | FIXED: names moved to tags, read by the importer and the POS. Non-ASCII tags UNKNOWN |
| Tax rates in the catalog; Deliveroo rounds to accepted rates | All | `vatRates` | PASS |
| Pull overwrites the connected catalog | Uber, Deliveroo | Never pull | CONFIG |
| Catalog must be pushed before orders flow | Just Eat | Bridge | CONFIG |
| Menu edited only on our side, menu editor blocked | Just Eat, Uber | Bridge, platform | CONFIG |
| Images: Just Eat 4:3 at least 1024x768, Deliveroo 1920x1080, Uber ratio 1:1 to 1.5:1 | All | `catalog_publish_images.go` | CONFIG: no single image fits all three |
| Deliveroo needs a menu description and a banner | Deliveroo | Back office | CONFIG |
| Opening hours and schedule overrides | All | No sync from pili-api | CONFIG: set in the bridge or platform; closing days set on the dashboard do not reach platforms |
| A `charge:` row for the restaurant's delivery fee | All | HubRise catalog charges are carried over, never created | CONFIG: create it in HubRise and map the bridge's delivery charge ref to it |

## To confirm with real data

Ask HubRise for sample orders from each bridge, or use the first real order:

1. The exact `channel`, `connection_name` and `created_by` of each bridge.
2. Whether `payments` is filled on an online order, and with which refs.
3. What arrives after a platform timeout.
4. Whether `restrictions.variant_refs` and `price_overrides` are honoured by each bridge.
5. Whether a `name:zh:寿司` category tag is accepted by the API (try on the test location).
6. On Uber with own couriers, whether the QR is enough without Delivery updates.
7. Whether an address only ever arrives on restaurant delivered orders: the charge rule and the driver screens rely on it.
