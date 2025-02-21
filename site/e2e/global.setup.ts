import { test, expect } from "@playwright/test"
import * as constants from "./constants"
import { STORAGE_STATE } from "./playwright.config"
import { Language } from "../src/components/CreateUserForm/CreateUserForm"

test("create first user", async ({ page }) => {
  await page.goto("/", { waitUntil: "networkidle" })

  await page.getByLabel(Language.usernameLabel).fill(constants.username)
  await page.getByLabel(Language.emailLabel).fill(constants.email)
  await page.getByLabel(Language.passwordLabel).fill(constants.password)
  await page.getByTestId("trial").click()
  await page.getByTestId("create").click()

  await expect(page).toHaveURL("/workspaces")

  await page.context().storageState({ path: STORAGE_STATE })
})
