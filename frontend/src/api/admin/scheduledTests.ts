/**
 * Admin Scheduled Tests API endpoints
 * Handles scheduled test plan management for account connectivity monitoring
 */

import { apiClient } from '../client'
import type {
  ScheduledTestPlan,
  ScheduledTestResult,
  CreateScheduledTestPlanRequest,
  UpdateScheduledTestPlanRequest,
  BatchCreateScheduledTestPlansRequest,
  BatchCreateScheduledTestPlansResponse
} from '@/types'

/**
 * List all scheduled test plans for an account
 * @param accountId - Account ID
 * @returns List of scheduled test plans
 */
export async function listByAccount(accountId: number): Promise<ScheduledTestPlan[]> {
  const { data } = await apiClient.get<ScheduledTestPlan[]>(
    `/admin/accounts/${accountId}/scheduled-test-plans`
  )
  return data ?? []
}

/**
 * Create a new scheduled test plan
 * @param req - Plan creation request
 * @returns Created plan
 */
export async function create(req: CreateScheduledTestPlanRequest): Promise<ScheduledTestPlan> {
  const { data } = await apiClient.post<ScheduledTestPlan>(
    '/admin/scheduled-test-plans',
    req
  )
  return data
}

/**
 * Batch create scheduled test plans across multiple accounts
 * @param req - Batch plan creation request
 * @returns Batch creation summary and per-account results
 */
export async function batchCreate(
  req: BatchCreateScheduledTestPlansRequest
): Promise<BatchCreateScheduledTestPlansResponse> {
  const { data } = await apiClient.post<BatchCreateScheduledTestPlansResponse>(
    '/admin/scheduled-test-plans/batch',
    req
  )
  return data
}

/**
 * Update an existing scheduled test plan
 * @param id - Plan ID
 * @param req - Fields to update
 * @returns Updated plan
 */
export async function update(id: number, req: UpdateScheduledTestPlanRequest): Promise<ScheduledTestPlan> {
  const { data } = await apiClient.put<ScheduledTestPlan>(
    `/admin/scheduled-test-plans/${id}`,
    req
  )
  return data
}

/**
 * Delete a scheduled test plan
 * @param id - Plan ID
 */
export async function deletePlan(id: number): Promise<void> {
  await apiClient.delete(`/admin/scheduled-test-plans/${id}`)
}

export const SCHEDULED_TEST_RUN_TIMEOUT_MS = 330000

/**
 * Execute a scheduled test plan immediately
 * @param id - Plan ID
 * @returns Executed test result
 */
export async function runNow(id: number): Promise<ScheduledTestResult> {
  const { data } = await apiClient.post<ScheduledTestResult>(
    `/admin/scheduled-test-plans/${id}/run`,
    undefined,
    { timeout: SCHEDULED_TEST_RUN_TIMEOUT_MS }
  )
  return data
}

/**
 * List test results for a plan
 * @param planId - Plan ID
 * @param limit - Optional max number of results to return
 * @returns List of test results
 */
export async function listResults(planId: number, limit?: number): Promise<ScheduledTestResult[]> {
  const { data } = await apiClient.get<ScheduledTestResult[]>(
    `/admin/scheduled-test-plans/${planId}/results`,
    {
      params: limit ? { limit } : undefined
    }
  )
  return data ?? []
}

export const scheduledTestsAPI = {
  listByAccount,
  create,
  batchCreate,
  update,
  delete: deletePlan,
  runNow,
  listResults
}

export default scheduledTestsAPI
