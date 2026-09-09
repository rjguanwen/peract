<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 64 64"
    xmlns="http://www.w3.org/2000/svg"
    class="logo-mark"
    aria-hidden="true"
  >
    <defs>
      <linearGradient :id="gradId" x1="0" y1="0" x2="1" y2="1">
        <stop offset="0" stop-color="#10b981" />
        <stop offset="1" stop-color="#0d9488" />
      </linearGradient>
    </defs>
    <rect x="2" y="2" width="60" height="60" rx="16" :fill="`url(#${gradId})`" />
    <g :transform="paperTransform">
      <rect x="10" y="18" width="24" height="31" rx="2.5" fill="#ffffff" opacity="0.30" />
      <path d="M14.5 26 H29.5 M14.5 32 H29.5 M14.5 38 H24" stroke="#ffffff" stroke-width="2" stroke-linecap="round" opacity="0.95" />
    </g>
    <path d="M23 44 L32.5 52 L53 23" fill="none" stroke="#ffffff" stroke-width="7" stroke-linecap="round" stroke-linejoin="round" />
  </svg>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  size: { type: [Number, String], default: 32 },
})

const uid = Math.random().toString(36).slice(2, 8)
const gradId = `peract-g-${uid}`
const paperTransform = computed(() => 'rotate(7 22 33)')
</script>

<style scoped>
.logo-mark {
  display: inline-block;
  vertical-align: middle;
}
</style>
