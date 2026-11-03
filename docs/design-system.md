# Twofold design system

> Visual language for the customer app and admin console.
> Open [`brand/palette.html`](brand/palette.html) in a browser to verify WCAG AA contrast.

## Brand

| | |
|---|---|
| **Name** | Twofold |
| **Tagline** | Every birr, recorded twice. |
| **Engineering line** | Money that always sums to zero. |
| **Voice** | Calm, precise, Ethiopian-local. Prefer plain words over fintech jargon. |

The brand signal on any first viewport must survive removing the nav. “Twofold” is the hero — not a tiny eyebrow.

## Colour

Named after highland ink and copper ledgers — not purple gradients, not cream paper, not newspaper chrome.

| Token | Hex | Use |
|---|---|---|
| `--ink` | `#07140F` | Primary text |
| `--ink-muted` | `#3D5248` | Secondary text |
| `--paper` | `#F2F5F3` | App background (cool stone, not cream) |
| `--surface` | `#FFFFFF` | Interactive surfaces only |
| `--line` | `#D5DED8` | Hairlines / dividers |
| `--highland` | `#0F4A38` | Brand primary (buttons, links) |
| `--highland-deep` | `#083024` | Pressed / header |
| `--copper` | `#B8893B` | Accent — fees, highlights, focus ring |
| `--success` | `#1B7A4A` | Credited / completed |
| `--warning` | `#A15C12` | Pending / hold |
| `--danger` | `#A11F2C` | Failed / freeze / error |
| `--ledger-debit` | `#8B2E2E` | Debit amounts |
| `--ledger-credit` | `#1B7A4A` | Credit amounts |

### Contrast (WCAG AA minimum)

| Pair | Ratio target | Passes when |
|---|---|---|
| `ink` on `paper` | ≥ 7:1 | Body text |
| `ink` on `surface` | ≥ 7:1 | Cards / forms |
| `highland` on `paper` | ≥ 4.5:1 | Links |
| White on `highland` | ≥ 4.5:1 | Primary buttons |
| `ink` on `copper` | ≥ 4.5:1 | Accent chips (use sparingly) |
| White on `danger` | ≥ 4.5:1 | Destructive buttons |

Verify live in `brand/palette.html`.

## Typography

Avoid Inter, Roboto, Arial, and system-only stacks.

| Role | Family | Fallback | Notes |
|---|---|---|---|
| Display / brand | **Fraunces** | Georgia, serif | Soft optical sizing; hero + receipts |
| UI / body | **Figtree** | system-ui, sans-serif | Dense forms, tables, admin |
| Mono / amounts | **IBM Plex Mono** | ui-monospace, monospace | Balances, fees, ledger lines |

### Scale (mobile-first)

| Token | Size / line | Use |
|---|---|---|
| `display` | 36 / 42 | Brand wordmark, empty states |
| `title` | 24 / 30 | Screen titles |
| `headline` | 18 / 24 | Section heads |
| `body` | 16 / 24 | Default |
| `caption` | 13 / 18 | Hints, timestamps |
| `amount-lg` | 32 / 36 mono | Home balance |
| `amount` | 16 / 20 mono | List rows |

## Money formatting

- Currency: **ETB** (Ethiopian Birr). MVP is single-currency.
- Always show two decimal places: `125.50 ETB`.
- Group thousands with commas: `1,250.00 ETB`.
- Debits prefixed with `−`, credits with `+` (or tinted, never ambiguous colour-only).
- Screen readers: announce as words where possible (“one hundred twenty-five birr and fifty cents”).
- Never round mid-calculation in UI — display server-authoritative amounts only.

## Motion

Ship presence, not noise. Customer app targets:

1. Balance card: soft fade-up on load (180–220ms, ease-out).
2. Send confirm: amount lock pulse when fee preview settles.
3. Receipt: stamp-in for status chip (completed / pending / failed).

Respect `prefers-reduced-motion`.

## Layout

- Customer app: mobile-first, max content width **480px**, full-bleed brand moments on marketing surfaces.
- Admin: denser tables, 12-column desktop grid, no marketing hero.
- Default: **no cards** unless the container is interactive. Prefer open sections + clear hierarchy.
- One job per screen: one headline, one short supporting line, one primary action.

## Components (MVP vocabulary)

| Component | Notes |
|---|---|
| `BalanceHero` | Large mono amount + available vs ledger when they differ |
| `FeePreview` | Shows fee + total before PIN; locks `expected_fee` |
| `PinPad` | 4-digit; lockout messaging after failures |
| `ReceiptSheet` | Debit/credit lines that sum to zero |
| `StatusChip` | Pending always includes reason + next step |
| `ActivityRow` | Counterparty, signed amount, relative time |
| `QrPay` | Merchant name + amount field or fixed amount |

## Do / don’t

| Do | Don’t |
|---|---|
| Put **Twofold** at hero scale on branded screens | Hide the brand in a 14px nav only |
| Use highland + copper with cool stone paper | Purple gradients, glow stacks, pill confetti |
| Show exact fees before confirm | Surprise totals after PIN |
| Explain pending states | Leave a spinner with no copy |
