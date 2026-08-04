<template>
  <NavigationProgress />
  <router-view />
  <Toast />
</template>

<script setup lang="ts">
import NavigationProgress from '@/components/common/NavigationProgress.vue'
import Toast from '@/components/common/Toast.vue'
</script>
