/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // 主色调 - 电流绿 (Instrument / Control Room signal accent)
        // 400/500 vivid for dark-mode accents & fills; 600+ darkened for text on light.
        primary: {
          50: '#f6ffe5',
          100: '#e9ffc2',
          200: '#d4fc92',
          300: '#bdf657',
          400: '#a8ec2f',
          500: '#9ef01a',
          600: '#5c8a0a',
          700: '#466b0b',
          800: '#36510f',
          900: '#2a3f10',
          950: '#16280a'
        },
        // 辅助色 - 冷中性灰 (secondary text / hairline borders)
        accent: {
          50: '#f6f8fa',
          100: '#eceff3',
          200: '#d5dbe2',
          300: '#b0bac6',
          400: '#7d8a99',
          500: '#5b6675',
          600: '#46505d',
          700: '#353d48',
          800: '#232a33',
          900: '#161b22',
          950: '#0d1117'
        },
        // 深色模式背景 - 墨蓝 ink/surface ramp (drives all dark:bg-dark-* surfaces)
        dark: {
          50: '#eef1f4',
          100: '#d7dde4',
          200: '#b8c0cb',
          300: '#8a96a6',
          400: '#56616f',
          500: '#3a4452',
          600: '#232b37',
          700: '#1b222e',
          800: '#151b25',
          900: '#0f141c',
          950: '#0a0e14'
        }
      },
      fontFamily: {
        sans: [
          'Hanken Grotesk Variable',
          'Hanken Grotesk',
          'system-ui',
          '-apple-system',
          'BlinkMacSystemFont',
          'Segoe UI',
          'Roboto',
          'PingFang SC',
          'Hiragino Sans GB',
          'Microsoft YaHei',
          'sans-serif'
        ],
        mono: ['IBM Plex Mono', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'monospace']
      },
      boxShadow: {
        glass: '0 8px 32px rgba(0, 0, 0, 0.08)',
        'glass-sm': '0 4px 16px rgba(0, 0, 0, 0.06)',
        glow: '0 0 20px rgba(158, 240, 26, 0.28)',
        'glow-lg': '0 0 40px rgba(158, 240, 26, 0.38)',
        card: '0 1px 3px rgba(0, 0, 0, 0.04), 0 1px 2px rgba(0, 0, 0, 0.06)',
        'card-hover': '0 10px 40px rgba(0, 0, 0, 0.08)',
        'inner-glow': 'inset 0 1px 0 rgba(255, 255, 255, 0.1)'
      },
      backgroundImage: {
        'gradient-radial': 'radial-gradient(var(--tw-gradient-stops))',
        'gradient-primary': 'linear-gradient(135deg, #9ef01a 0%, #5c8a0a 100%)',
        'gradient-dark': 'linear-gradient(135deg, #151b25 0%, #0a0e14 100%)',
        'gradient-glass':
          'linear-gradient(135deg, rgba(255,255,255,0.1) 0%, rgba(255,255,255,0.05) 100%)',
        'mesh-gradient':
          'radial-gradient(at 40% 20%, rgba(20, 184, 166, 0.12) 0px, transparent 50%), radial-gradient(at 80% 0%, rgba(6, 182, 212, 0.08) 0px, transparent 50%), radial-gradient(at 0% 50%, rgba(20, 184, 166, 0.08) 0px, transparent 50%)'
      },
      animation: {
        'fade-in': 'fadeIn 0.3s ease-out',
        'slide-up': 'slideUp 0.3s ease-out',
        'slide-down': 'slideDown 0.3s ease-out',
        'slide-in-right': 'slideInRight 0.3s ease-out',
        'scale-in': 'scaleIn 0.2s ease-out',
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        shimmer: 'shimmer 2s linear infinite',
        glow: 'glow 2s ease-in-out infinite alternate'
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' }
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideDown: {
          '0%': { opacity: '0', transform: 'translateY(-10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideInRight: {
          '0%': { opacity: '0', transform: 'translateX(20px)' },
          '100%': { opacity: '1', transform: 'translateX(0)' }
        },
        scaleIn: {
          '0%': { opacity: '0', transform: 'scale(0.95)' },
          '100%': { opacity: '1', transform: 'scale(1)' }
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' }
        },
        glow: {
          '0%': { boxShadow: '0 0 20px rgba(158, 240, 26, 0.28)' },
          '100%': { boxShadow: '0 0 30px rgba(158, 240, 26, 0.42)' }
        }
      },
      backdropBlur: {
        xs: '2px'
      },
      borderRadius: {
        // tightened for the instrument aesthetic (sharper corners, hairline feel)
        lg: '0.375rem',
        xl: '0.4rem',
        '2xl': '0.5rem',
        '3xl': '0.625rem',
        '4xl': '0.75rem'
      }
    }
  },
  plugins: []
}
