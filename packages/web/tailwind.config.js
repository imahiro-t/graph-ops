import plugin from 'tailwindcss/plugin'

/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {},
  },
  plugins: [
    // Named rem media-query variants (DFLT-00260). They replace the
    // arbitrary [@media_not_all_and_(min-width:80rem)]: and
    // [@media(max-width:15rem)]: prefixes with the same media conditions.
    // rem, not px, so the breakpoints follow the browser's default font size
    // (DFLT-00253). They are variants rather than theme.screens entries:
    // Tailwind 3.4 drops the max-* screen variants (max-sm:, max-lg:, ...)
    // once px and rem screens are mixed.
    //
    // Ordering caveat: the registration order below is the order their rules
    // come out in the CSS, so when both set the same property the later one
    // wins. below-80rem is registered first so that the narrower upto-15rem
    // overrides it (e.g. `below-80rem:gap-x-3 upto-15rem:gap-x-1` gives
    // gap-x-1 at 15rem and below). Both are emitted BEFORE the core screen
    // variants (sm:, max-sm:, max-lg:, ...), @container variants and any
    // remaining arbitrary [@media(...)]: variants, so if one of those sets
    // the same property on the same element it wins even when its condition
    // is wider (`max-sm:p-3 upto-15rem:p-2` still gives p-3). Keep such
    // pairs to different properties or mutually exclusive conditions, or
    // check the built CSS order when combining them.
    plugin(({ addVariant }) => {
      addVariant('below-80rem', '@media not all and (min-width: 80rem)')
      addVariant('upto-15rem', '@media (max-width: 15rem)')
    }),
  ],
}
