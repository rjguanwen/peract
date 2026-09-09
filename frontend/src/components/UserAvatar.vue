<template>
  <el-avatar v-if="src" :src="src" :size="size" :style="avatarStyle" class="user-avatar" />
  <el-avatar v-else :size="size" :style="avatarStyle" class="user-avatar">
    {{ initials }}
  </el-avatar>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  src: { type: String, default: '' },
  name: { type: String, default: '' },
  size: { type: [Number, String], default: 32 },
  color: { type: String, default: '' },
})

const colors = ['#3b5bdb', '#0ca678', '#e8590c', '#7048e8', '#2b8a3e', '#c2255c', '#1971c2', '#f08c00']
const initials = computed(() => {
  const n = props.name.trim()
  if (!n) return '?'
  return Array.from(n)[0].toUpperCase()
})
const avatarStyle = computed(() => {
  if (props.color) return { background: props.color }
  let hash = 0
  const n = props.name.trim()
  for (let i = 0; i < n.length; i++) hash = (hash * 31 + n.charCodeAt(i)) >>> 0
  return { background: colors[hash % colors.length] }
})
</script>

<style scoped>
.user-avatar {
  flex-shrink: 0;
  user-select: none;
}
</style>
