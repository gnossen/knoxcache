@Library("artifact-store-library") _

pipeline {
    agent { label "common" }

    post {
        always {
            deleteDir()
        }
    }

    options {
        disableConcurrentBuilds()
    }

    stages {
        stage('Build Browser Extensions') {
            steps {
                // TODO: Only when there are changes to it.
                sh "docker pull docker:5000/build-image:latest"
                sh "docker run -v `pwd`:/build -e \"BUILD_NUMBER=${BUILD_NUMBER}\" docker:5000/build-image:latest bash -c 'cd /build/chromium-extension && make'"
                sh "docker run -v `pwd`:/build -e \"BUILD_NUMBER=${BUILD_NUMBER}\" docker:5000/build-image:latest bash -c 'cd /build/firefox-addon && make'"
            }
        }

        stage('Publish Browser Extensions') {
            // TODO: Only when there are changes to it.
            when { branch "main" }
            environment {
                ART_STORE = credentials("artifact-store")
            }
            steps {
                script {
                    chromiumVersion = sh(script: "cd chromium-extension; make print-version", returnStdout: true).trim()
                    chromiumArtifactName = "knox-chromium-extension-${chromiumVersion}.zip"
                    chromiumRemoteDirectory = "knox/chromium-extension"
                    uploadArtifact("chromium-extension/${chromiumArtifactName}", chromiumRemoteDirectory, env.ART_STORE_USR, env.ART_STORE_PSW)
                    updateLink("knox/chromium-extension", chromiumArtifactName, "chromium-extension-latest.zip", env.ART_STORE_USR, env.ART_STORE_PSW)

                    // TODO: Firefox.
                }
            }
        }
    }
}
