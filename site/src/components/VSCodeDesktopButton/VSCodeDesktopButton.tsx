import { FC, PropsWithChildren, useState, useRef } from "react"
import { getApiKey } from "api/api"
import { VSCodeIcon } from "components/Icons/VSCodeIcon"
import { VSCodeInsidersIcon } from "components/Icons/VSCodeInsidersIcon"
import { PrimaryAgentButton } from "components/Resources/AgentButton"
import KeyboardArrowDownIcon from "@mui/icons-material/KeyboardArrowDown"
import ButtonGroup from "@mui/material/ButtonGroup"
import { useLocalStorage } from "hooks"
import Menu from "@mui/material/Menu"
import MenuItem from "@mui/material/MenuItem"

export interface VSCodeDesktopButtonProps {
  userName: string
  workspaceName: string
  agentName?: string
  folderPath?: string
}

type VSCodeVariant = "vscode" | "vscode-insiders"

const VARIANT_KEY = "vscode-variant"

export const VSCodeDesktopButton: FC<
  PropsWithChildren<VSCodeDesktopButtonProps>
> = (props) => {
  const [isVariantMenuOpen, setIsVariantMenuOpen] = useState(false)
  const localStorage = useLocalStorage()
  const previousVariant = localStorage.getLocal(VARIANT_KEY)
  const [variant, setVariant] = useState<VSCodeVariant>(() => {
    if (!previousVariant) {
      return "vscode"
    }
    return previousVariant as VSCodeVariant
  })
  const menuAnchorRef = useRef<HTMLDivElement>(null)

  const selectVariant = (variant: VSCodeVariant) => {
    localStorage.saveLocal(VARIANT_KEY, variant)
    setVariant(variant)
    setIsVariantMenuOpen(false)
  }

  return (
    <div>
      <ButtonGroup
        ref={menuAnchorRef}
        variant="outlined"
        sx={{
          // Workaround to make the border transitions smmothly on button groups
          "& > button:hover + button": {
            borderLeft: "1px solid #FFF",
          },
        }}
      >
        {variant === "vscode" ? (
          <VSCodeButton {...props} />
        ) : (
          <VSCodeInsidersButton {...props} />
        )}

        <PrimaryAgentButton
          aria-controls={
            isVariantMenuOpen ? "vscode-variant-button-menu" : undefined
          }
          aria-expanded={isVariantMenuOpen ? "true" : undefined}
          aria-label="select VSCode variant"
          aria-haspopup="menu"
          disableRipple
          onClick={() => {
            setIsVariantMenuOpen(true)
          }}
          sx={{ px: 0 }}
        >
          <KeyboardArrowDownIcon sx={{ fontSize: 16 }} />
        </PrimaryAgentButton>
      </ButtonGroup>

      <Menu
        open={isVariantMenuOpen}
        anchorEl={menuAnchorRef.current}
        onClose={() => setIsVariantMenuOpen(false)}
        sx={{
          "& .MuiMenu-paper": {
            width: menuAnchorRef.current?.clientWidth,
          },
        }}
      >
        <MenuItem
          sx={{ fontSize: 14 }}
          onClick={() => {
            selectVariant("vscode")
          }}
        >
          <VSCodeIcon sx={{ width: 12, height: 12 }} />
          VS Code Desktop
        </MenuItem>
        <MenuItem
          sx={{ fontSize: 14 }}
          onClick={() => {
            selectVariant("vscode-insiders")
          }}
        >
          <VSCodeInsidersIcon sx={{ width: 12, height: 12 }} />
          VS Code Insiders
        </MenuItem>
      </Menu>
    </div>
  )
}

const VSCodeButton = ({
  userName,
  workspaceName,
  agentName,
  folderPath,
}: VSCodeDesktopButtonProps) => {
  const [loading, setLoading] = useState(false)

  return (
    <PrimaryAgentButton
      startIcon={<VSCodeIcon />}
      disabled={loading}
      onClick={() => {
        setLoading(true)
        getApiKey()
          .then(({ key }) => {
            const query = new URLSearchParams({
              owner: userName,
              workspace: workspaceName,
              url: location.origin,
              token: key,
            })
            if (agentName) {
              query.set("agent", agentName)
            }
            if (folderPath) {
              query.set("folder", folderPath)
            }

            location.href = `vscode://coder.coder-remote/open?${query.toString()}`
          })
          .catch((ex) => {
            console.error(ex)
          })
          .finally(() => {
            setLoading(false)
          })
      }}
    >
      VS Code Desktop
    </PrimaryAgentButton>
  )
}

const VSCodeInsidersButton = ({
  userName,
  workspaceName,
  agentName,
  folderPath,
}: VSCodeDesktopButtonProps) => {
  const [loading, setLoading] = useState(false)

  return (
    <PrimaryAgentButton
      startIcon={<VSCodeInsidersIcon />}
      disabled={loading}
      onClick={() => {
        setLoading(true)
        getApiKey()
          .then(({ key }) => {
            const query = new URLSearchParams({
              owner: userName,
              workspace: workspaceName,
              url: location.origin,
              token: key,
            })
            if (agentName) {
              query.set("agent", agentName)
            }
            if (folderPath) {
              query.set("folder", folderPath)
            }

            location.href = `vscode-insiders://coder.coder-remote/open?${query.toString()}`
          })
          .catch((ex) => {
            console.error(ex)
          })
          .finally(() => {
            setLoading(false)
          })
      }}
    >
      VS Code Insiders
    </PrimaryAgentButton>
  )
}
