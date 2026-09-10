/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // Construction-industry, trustworthy palette. 500/600/700 are
        // Tailwind blue-600/700/800 (the app + genicons gradient anchors);
        // 200-400/800-900 complete the ramp on the same shifted scale
        // (brand-N ≈ blue-(N+100) for N>=200), 50/100 stay the custom tints.
        brand: {
          50: '#eef6ff',
          100: '#d9eaff',
          200: '#93c5fd',
          300: '#60a5fa',
          400: '#3b82f6',
          500: '#2563eb',
          600: '#1d4ed8',
          700: '#1e40af',
          800: '#1e3a8a',
          900: '#172554',
        },
      },
    },
  },
  plugins: [],
}
