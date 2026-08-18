<template>
  <el-tag :type="meta.type" effect="plain" round>
    {{ meta.label }}
  </el-tag>
</template>

<script setup>
import { computed } from 'vue'
import { priorityMeta } from '../utils/constants'

const props = defineProps({
  priority: { type: String, required: true },
})

const meta = computed(() => priorityMeta(props.priority))
</script>
