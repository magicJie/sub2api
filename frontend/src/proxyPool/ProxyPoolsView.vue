<template>
  <!-- 代理池（fork）：侧边栏“代理池”入口对应的独立页面 -->
  <AppLayout>
    <div class="card p-4">
      <ProxyPoolManagerPanel />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import ProxyPoolManagerPanel from './components/ProxyPoolManagerPanel.vue'
</script>
