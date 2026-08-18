<template>
  <el-tag :type="meta.type" effect="light" round>
    {{ meta.label }}
  </el-tag>
</template>

<script setup>
import { computed } from 'vue'
import { statusMeta } from '../utils/constants'

const props = defineProps({
  status: { type: String, required: true },
})

const meta = computed(() => statusMeta(props.status))
</script>
